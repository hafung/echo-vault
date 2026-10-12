package main

import (
	"context"
	"net/url"
	"os"
	"testing"
)

func TestVisibilitySearchOrderAndSyncPersistence(t *testing.T) {
	s := testStore(t)
	for _, v := range []M{
		{"id": "old1", "project": "old", "updated": 10}, {"id": "old2", "project": "old", "updated": 20},
		{"id": "new", "project": "new", "updated": 30}, {"id": "archive", "project": "archived", "updated": 40, "archived": 1},
	} {
		id := str(v["id"])
		v["created"] = 1
		v["title"] = "共同关键词"
		v["favorite"] = 1
		check(t, s.session(s.Write, id, v))
		_, err := s.message(s.Write, id, "message", 0, "user", "跨项目 搜索测试", 1, "message", "", M{})
		check(t, err)
	}
	stats, err := s.stats(context.Background())
	check(t, err)
	projects := stats["projects_list"].([]M)
	if len(projects) != 2 || str(projects[0]["project"]) != "new" {
		t.Fatalf("项目没有按最新更新时间排序: %v", projects)
	}
	list := func(params url.Values, want int) {
		t.Helper()
		v, err := s.sessions(context.Background(), params)
		check(t, err)
		if len(v["items"].([]M)) != want {
			t.Fatalf("%v: want %d, got %v", params, want, v)
		}
	}
	list(url.Values{"q": {"搜索测试"}}, 3)
	list(url.Values{"q": {"搜索测试"}, "project": {"new"}}, 1)
	app := &App{Store: s}
	_, err = s.Write.Exec("UPDATE sessions SET favorite=1")
	check(t, err)
	for _, request := range []struct{ path, body string }{
		{"/api/sessions/new/visibility", `{"hidden":true}`},
		{"/api/projects/visibility", `{"project":"old","hidden":true}`},
	} {
		res := backupRequest(t, app, "POST", request.path, request.body)
		if res.Code != 200 {
			t.Fatal(res.Body.String())
		}
	}
	list(url.Values{"q": {"搜索测试"}}, 0)
	list(url.Values{"q": {"搜索测试"}, "hidden": {"1"}}, 4)
	if res := backupRequest(t, app, "GET", "/api/sessions/new", ""); res.Code != 404 {
		t.Fatal("默认阅读范围展示了隐藏对话", res.Body.String())
	}
	if res := backupRequest(t, app, "GET", "/api/sessions/new?hidden=1", ""); res.Code != 200 {
		t.Fatal("查看全部无法打开隐藏对话", res.Body.String())
	}
	list(url.Values{"favorite": {"1"}}, 0)
	stats, err = s.stats(context.Background())
	check(t, err)
	if integer(stats["sessions"]) != 0 || len(stats["projects_list"].([]M)) != 0 || integer(stats["favorites"]) != 0 {
		t.Fatal("隐藏后的导航数量不一致", stats)
	}
	stats, err = s.stats(context.Background(), true)
	check(t, err)
	if integer(stats["sessions"]) != 4 || integer(stats["favorites"]) != 4 {
		t.Fatal("查看全部没有包含隐藏数据", stats)
	}
	// Full and incremental imports use the same metadata upsert. Local choices
	// survive source metadata updates, and a hidden project's new sessions hide too.
	check(t, s.session(s.Write, "new", M{"title": "新标题", "project": "new", "archived": 0}))
	check(t, s.session(s.Write, "later", M{"title": "后续对话", "project": "old"}))
	list(url.Values{}, 0)
	list(url.Values{"hidden": {"1"}}, 5)
	for _, request := range []struct{ path, body string }{
		{"/api/sessions/new/visibility", `{"hidden":false}`},
		{"/api/sessions/archive/visibility", `{"hidden":false}`},
		{"/api/projects/visibility", `{"project":"old","hidden":false}`},
	} {
		if res := backupRequest(t, app, "POST", request.path, request.body); res.Code != 200 {
			t.Fatal(res.Body.String())
		}
	}
	check(t, s.session(s.Write, "archive", M{"archived": 1}))
	list(url.Values{}, 5)
	// Run the actual importer twice, including a forced full source scan.
	entry := fixture(t, t.TempDir(), "real", responseEvent("user", "同步后的隐藏对话"))
	i := newImporter(s)
	check(t, i.rollout(entry, nil, false))
	res := backupRequest(t, app, "POST", "/api/sessions/real/visibility", `{"hidden":true}`)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	f, err := os.OpenFile(entry.path, os.O_APPEND|os.O_WRONLY, 0600)
	check(t, err)
	_, err = f.WriteString(jsonText(responseEvent("assistant", "新增正文")) + "\n")
	check(t, err)
	check(t, f.Close())
	check(t, i.rollout(entry, nil, false))
	check(t, i.rollout(entry, nil, true))
	list(url.Values{"project": {"garden"}}, 0)
	list(url.Values{"project": {"garden"}, "hidden": {"1"}}, 1)
}
