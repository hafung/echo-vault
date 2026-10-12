package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDeleteSessionRequiresConfirmationAndCleansArchive(t *testing.T) {
	s := testStore(t)
	app := &App{Store: s}
	for _, sid := range []string{"delete", "keep"} {
		check(t, s.session(s.Write, sid, M{"title": sid, "project": sid}))
		_, err := s.message(s.Write, sid, sid, 0, "user", sid+" needle 中文", 1, "message", "", M{"assets": []any{"shared", sid}})
		check(t, err)
		check(t, s.addRaw(s.Write, sid, sid, 0, 0, 6, []byte("shared")))
		check(t, s.addRaw(s.Write, sid+"-unique", sid, 0, 0, int64(len(sid)), []byte(sid)))
		check(t, s.recount(s.Write, sid))
		_, err = s.Write.Exec("INSERT INTO files(path,session_id) VALUES (?,?); INSERT INTO session_visibility VALUES (?,0); INSERT INTO db_items VALUES ('source',?,?,'marker')", sid, sid, sid, sid, sid)
		check(t, err)
	}
	_, err := s.Write.Exec("INSERT INTO assets VALUES ('shared','image/png',X'01'),('delete','image/png',X'02'),('keep','image/png',X'03'); UPDATE sessions SET favorite=1 WHERE id='delete'")
	check(t, err)
	for _, body := range []string{`{}`, `{"confirmed":false}`, `{"confirmed":"true"}`} {
		if res := backupRequest(t, app, "POST", "/api/sessions/delete/delete", body); res.Code != 400 {
			t.Fatal("删除必须确认", res.Code, res.Body.String())
		}
	}
	if count(t, s, "sessions") != 2 || count(t, s, "deleted_sessions") != 0 {
		t.Fatal("未确认的请求修改了档案")
	}
	if res := backupRequest(t, app, "POST", "/api/sessions/delete/delete", `{"confirmed":true}`); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	for _, table := range []string{"sessions", "messages", "files", "raw_chunks", "session_visibility", "db_items", "deleted_sessions"} {
		want := 1
		if table == "raw_chunks" {
			want = 2
		}
		if got := count(t, s, table); got != want {
			t.Fatalf("%s: got %d, want %d", table, got, want)
		}
	}
	if count(t, s, "assets") != 2 || count(t, s, "raw_blobs") != 2 {
		t.Fatal("没有清理独占数据，或误删了共享数据")
	}
	for _, table := range []string{"search_long", "search_short", "dialogue_long", "dialogue_short"} {
		if count(t, s, table) != 1 {
			t.Fatal("未清理搜索索引", table)
		}
	}
	for _, hidden := range []string{"0", "1"} {
		list, err := s.sessions(context.Background(), url.Values{"q": {"needle 中文"}, "hidden": {hidden}})
		check(t, err)
		if items := list["items"].([]M); len(items) != 1 || str(items[0]["id"]) != "keep" {
			t.Fatal("搜索仍返回已删除的会话", list)
		}
		stats, err := s.stats(context.Background(), hidden == "1")
		check(t, err)
		if integer(stats["sessions"]) != 1 || integer(stats["favorites"]) != 0 || len(stats["projects_list"].([]M)) != 1 || str(stats["projects_list"].([]M)[0]["project"]) != "keep" {
			t.Fatal("空项目或计数未清理", stats)
		}
		if res := backupRequest(t, app, "GET", "/api/sessions/delete?hidden="+hidden, ""); res.Code != 404 {
			t.Fatal("仍能读取已删除会话", res.Body.String())
		}
	}
	if res := backupRequest(t, app, "POST", "/api/sessions/delete/delete", `{"confirmed":true}`); res.Code != 404 {
		t.Fatal("不存在的会话应该返回 404", res.Code)
	}
	check(t, s.deleteSession(context.Background(), "keep"))
	if count(t, s, "assets") != 0 || count(t, s, "raw_blobs") != 0 {
		t.Fatal("最后一个引用删除后共享数据没有清理")
	}
}

func TestDeleteSessionRollsBackOnFailure(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	check(t, i.rollout(fixture(t, t.TempDir(), "rollback", responseEvent("user", "仍然保留")), nil, false))
	_, err := s.Write.Exec("CREATE TRIGGER reject_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT,'blocked'); END")
	check(t, err)
	if err := s.deleteSession(context.Background(), "rollback"); err == nil {
		t.Fatal("删除失败应返回错误")
	}
	for _, table := range []string{"sessions", "messages", "search_long", "dialogue_long", "raw_blobs", "files"} {
		if count(t, s, table) != 1 {
			t.Fatal("失败时没有回滚", table)
		}
	}
	if count(t, s, "deleted_sessions") != 0 {
		t.Fatal("失败时留下了删除标记")
	}
}

func TestDeletedRolloutStaysDeletedAcrossRestartUntilFullScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "echo.sqlite")
	s, err := openStore(path)
	check(t, err)
	t.Cleanup(func() { s.Close() })
	entry := fixture(t, t.TempDir(), "rollout", responseEvent("user", "原始正文"))
	i := newImporter(s)
	check(t, i.rollout(entry, nil, false))
	check(t, s.deleteSession(context.Background(), "rollout"))
	s.Close()
	s, err = openStore(path)
	check(t, err)
	i = newImporter(s)
	f, err := os.OpenFile(entry.path, os.O_APPEND|os.O_WRONLY, 0600)
	check(t, err)
	_, err = f.WriteString(jsonText(responseEvent("assistant", "追加正文")) + "\n")
	check(t, err)
	check(t, f.Close())
	check(t, i.rollout(entry, nil, false))
	// A renamed or mirrored source with the same ID must also remain excluded.
	mirror := fixture(t, t.TempDir(), "rollout", responseEvent("user", "镜像正文"))
	check(t, i.rollout(mirror, nil, false))
	if count(t, s, "sessions") != 0 || count(t, s, "messages") != 0 || count(t, s, "raw_chunks") != 0 || count(t, s, "deleted_sessions") != 1 {
		t.Fatal("增量同步重新导入已删除会话")
	}
	check(t, i.rollout(entry, nil, true))
	if count(t, s, "sessions") != 1 || count(t, s, "messages") != 2 || count(t, s, "deleted_sessions") != 0 {
		t.Fatal("完整扫描未恢复全部正文")
	}
	check(t, i.rollout(entry, nil, false))
	if count(t, s, "messages") != 2 {
		t.Fatal("恢复后的增量同步产生重复")
	}
	check(t, s.deleteSession(context.Background(), "rollout"))
	check(t, os.Remove(entry.path))
	if err = i.rollout(entry, nil, true); err == nil || count(t, s, "deleted_sessions") != 1 {
		t.Fatal("不可用来源不应清除删除标记")
	}
	check(t, i.rollout(mirror, nil, false))
	if count(t, s, "sessions") != 0 {
		t.Fatal("扫描失败后增量同步不应恢复会话")
	}
}

func TestDeletedHistoryOnlyReturnsOnFullScan(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	path := filepath.Join(t.TempDir(), "thread_history.sqlite")
	source, err := sql.Open("sqlite", path)
	check(t, err)
	defer source.Close()
	_, err = source.Exec("CREATE TABLE thread_items(thread_id TEXT,item_id TEXT,rollout_ordinal INTEGER,created_at_ms INTEGER,item_json TEXT,updated_at_ordinal INTEGER)")
	check(t, err)
	add := func(sid, id string, ordinal int) {
		_, err := source.Exec("INSERT INTO thread_items VALUES (?,?,?,?,?,?)", sid, id, ordinal, 1767225600000, jsonText(M{"id": id, "type": "userMessage", "content": []any{M{"text": id}}}), ordinal)
		check(t, err)
	}
	add("deleted-db", "first", 0)
	entry := rolloutFile{path: path, source: Source{Path: filepath.Dir(path), Platform: "WSL"}}
	check(t, i.history(entry, nil, false))
	check(t, s.deleteSession(context.Background(), "deleted-db"))
	add("deleted-db", "second", 1)
	add("keep-db", "other", 0)
	check(t, i.history(entry, nil, false))
	if count(t, s, "sessions") != 1 || count(t, s, "messages") != 1 || count(t, s, "deleted_sessions") != 1 {
		t.Fatal("历史数据库增量同步未过滤删除会话")
	}
	check(t, i.history(entry, nil, true))
	if count(t, s, "sessions") != 2 || count(t, s, "messages") != 3 || count(t, s, "deleted_sessions") != 0 {
		t.Fatal("历史数据库完整扫描未恢复会话")
	}
}

func TestDeletedChatGPTOnlyReturnsOnFullScan(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	path := filepath.Join(t.TempDir(), "conversations.json")
	conversation := func(id, text string) M {
		return M{"id": id, "title": id, "mapping": M{"user": M{"message": M{"author": M{"role": "user"}, "content": M{"parts": []any{text}}}}}}
	}
	check(t, os.WriteFile(path, []byte(jsonText([]any{conversation("deleted", "first")})), 0600))
	check(t, i.chatgpt(path, false))
	check(t, s.deleteSession(context.Background(), "chatgpt:deleted"))
	check(t, os.WriteFile(path, []byte(jsonText([]any{conversation("deleted", "changed"), conversation("keep", "other")})), 0600))
	check(t, i.chatgpt(path, false))
	if count(t, s, "sessions") != 1 || count(t, s, "messages") != 1 || count(t, s, "deleted_sessions") != 1 {
		t.Fatal("ChatGPT 增量同步未过滤删除会话")
	}
	check(t, i.chatgpt(path, true))
	if count(t, s, "sessions") != 2 || count(t, s, "messages") != 2 || count(t, s, "deleted_sessions") != 0 {
		t.Fatal("ChatGPT 完整扫描未恢复会话")
	}
}

func TestDeletionMarkersSurviveBackupAndRestore(t *testing.T) {
	s := testStore(t)
	entry := fixture(t, t.TempDir(), "backed-up-deletion", responseEvent("user", "已删除的正文"))
	check(t, newImporter(s).rollout(entry, nil, false))
	check(t, s.deleteSession(context.Background(), "backed-up-deletion"))
	backup := filepath.Join(t.TempDir(), "backup.sqlite")
	check(t, s.backupTo(context.Background(), backup, nil))
	path := filepath.Join(t.TempDir(), "restored.sqlite")
	check(t, restoreArchive(context.Background(), backup, path, nil))
	restored, err := openStore(path)
	check(t, err)
	defer restored.Close()
	check(t, newImporter(restored).rollout(entry, nil, false))
	if count(t, restored, "sessions") != 0 || count(t, restored, "deleted_sessions") != 1 {
		t.Fatal("备份恢复丢失删除标记")
	}
}
