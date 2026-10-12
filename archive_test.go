package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := openStore(filepath.Join(t.TempDir(), "echo.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func fixture(t *testing.T, dir, sid string, events ...M) rolloutFile {
	t.Helper()
	os.MkdirAll(dir, 0700)
	p := filepath.Join(dir, sid+".jsonl")
	f, err := os.Create(p)
	check(t, err)
	enc := json.NewEncoder(f)
	check(t, enc.Encode(M{"type": "session_meta", "payload": M{"id": sid, "cwd": "/home/me/wwwroot/garden", "timestamp": "2026-01-01T00:00:00Z"}}))
	for _, e := range events {
		check(t, enc.Encode(e))
	}
	check(t, f.Close())
	return rolloutFile{p, Source{dir, "WSL", true, "Codex"}, false}
}
func responseEvent(role, text string) M {
	return M{"type": "response_item", "timestamp": "2026-01-02T00:00:00Z", "payload": M{"type": "message", "role": role, "content": []any{M{"type": "input_text", "text": text}}}}
}
func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	check(t, s.DB.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
	return n
}

func TestRolloutIncrementalPreservesDeletedAndTruncatedSource(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	entry := fixture(t, t.TempDir(), "session-one", responseEvent("user", "中文搜索 SQLite archive"), responseEvent("assistant", "保留历史，丝滑体验"))
	original, err := os.ReadFile(entry.path)
	check(t, err)
	check(t, i.rollout(entry, map[string]M{}, false))
	after, _ := os.ReadFile(entry.path)
	if !bytes.Equal(original, after) {
		t.Fatal("原始文件被修改")
	}
	if count(t, s, "messages") != 2 {
		t.Fatal("正文导入失败")
	}
	check(t, i.rollout(entry, map[string]M{}, false))
	if count(t, s, "messages") != 2 {
		t.Fatal("重复同步不幂等")
	}
	if i.status().Skipped != 1 {
		t.Fatal("纳秒时间戳必须精确比较，未变化文件不应重读")
	}
	f, err := os.OpenFile(entry.path, os.O_APPEND|os.O_WRONLY, 0600)
	check(t, err)
	check(t, json.NewEncoder(f).Encode(responseEvent("user", "新增一条")))
	f.Close()
	check(t, i.rollout(entry, map[string]M{}, false))
	if count(t, s, "messages") != 3 {
		t.Fatal("增量同步失败")
	}
	check(t, i.rollout(entry, map[string]M{}, true))
	var fullRaw bytes.Buffer
	check(t, s.export("session-one", "raw", &fullRaw))
	latest, _ := os.ReadFile(entry.path)
	if !bytes.Equal(fullRaw.Bytes(), latest) {
		t.Fatal("追加后完整扫描产生重复原始字节")
	}
	// Simulate a source rewrite: imported prior messages are never removed.
	check(t, os.WriteFile(entry.path, original, 0600))
	check(t, i.rollout(entry, map[string]M{}, false))
	if count(t, s, "messages") != 3 {
		t.Fatal("源截断造成历史丢失")
	}
	check(t, os.Remove(entry.path))
	if count(t, s, "messages") != 3 {
		t.Fatal("源删除造成档案丢失")
	}
	page, err := s.messages(context.Background(), "session-one", url.Values{})
	check(t, err)
	if page["total"] != 3 {
		t.Fatal("已删除源的历史不可读")
	}
	for _, term := range []string{"中", "中文", "中文搜索", "sqlite", "保留", "丝滑", "中文 sqlite", "\"SQLite archive\""} {
		result, err := s.sessions(context.Background(), url.Values{"q": {term}})
		check(t, err)
		if len(result["items"].([]M)) != 1 {
			t.Fatalf("搜索 %q 未命中", term)
		}
	}
	result, err := s.sessions(context.Background(), url.Values{"q": {"\" OR 1=1 --"}})
	check(t, err)
	if len(result["items"].([]M)) != 0 {
		t.Fatal("查询未按字面值处理")
	}
}

func TestRawDeduplicationKeepsVersionsAndExactNanosecondIntegers(t *testing.T) {
	n := int64(1790928314787737111)
	if integer(n) != n {
		t.Fatal("纳秒整数比较丢失精度")
	}
	s := testStore(t)
	check(t, s.session(s.Write, "s", M{"title": "原始历史"}))
	raw := []byte("原始字节\n")
	for _, generation := range []int64{0, 1, 2} {
		_, e := s.Write.Exec("INSERT INTO raw_chunks(path,session_id,generation,start,end,payload) VALUES (?,?,?,?,?,?)", "file", "s", generation, 0, len(raw), pack(raw))
		check(t, e)
	}
	check(t, s.deduplicate(nil))
	if count(t, s, "raw_chunks") != 3 || count(t, s, "raw_blobs") != 1 {
		t.Fatal("去重没有完整保留逻辑版本")
	}
	var export bytes.Buffer
	check(t, s.export("s", "raw", &export))
	if !bytes.Equal(export.Bytes(), raw) {
		t.Fatal("去重改变原始字节")
	}
}

func TestIncompleteTailAndCheckpointResume(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	events := []M{}
	for n := 0; n < 270; n++ {
		events = append(events, responseEvent("user", "反复相同但是真实的用户消息"))
	}
	entry := fixture(t, t.TempDir(), "long-session", events...)
	f, err := os.OpenFile(entry.path, os.O_APPEND|os.O_WRONLY, 0600)
	check(t, err)
	partial := []byte(`{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"尾行`)
	f.Write(partial)
	f.Close()
	check(t, i.rollout(entry, map[string]M{}, false))
	if count(t, s, "messages") != 270 {
		t.Fatal("相同消息被误去重或未完成尾行被导入")
	}
	f, _ = os.OpenFile(entry.path, os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString("完成\"}]}}\n")
	f.Close()
	check(t, i.rollout(entry, map[string]M{}, false))
	if count(t, s, "messages") != 271 {
		t.Fatal("未完成尾行没有在下次同步补齐")
	}
	var raw bytes.Buffer
	check(t, s.export("long-session", "raw", &raw))
	original, _ := os.ReadFile(entry.path)
	if !bytes.Equal(raw.Bytes(), original) {
		t.Fatal("原始文件重建字节不一致")
	}
	page, err := s.messages(context.Background(), "long-session", url.Values{"offset": {"260"}})
	check(t, err)
	if len(page["items"].([]M)) != 11 {
		t.Fatal("长对话分页失败")
	}
}

func TestCanonicalItemsAndDatabaseProjectionDoNotDuplicate(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	user := M{"type": "userMessage", "id": "user-a", "content": []any{M{"type": "text", "text": "完整正文"}}}
	agent := M{"type": "agentMessage", "id": "agent-a", "text": "完整回答"}
	entry := fixture(t, t.TempDir(), "canonical", M{"type": "event_msg", "payload": M{"type": "item_completed", "item": user}}, responseEvent("user", "完整正文"), M{"type": "event_msg", "payload": M{"type": "item_completed", "item": agent}}, responseEvent("assistant", "完整回答"))
	metadata := map[string]M{"canonical": {"history_mode": "paginated", "cwd": "/home/me/wwwroot/garden", "title": "正式标题", "created_at": int64(1767225600)}}
	check(t, i.rollout(entry, metadata, false))
	if count(t, s, "messages") != 2 {
		t.Fatal("新版事件与 response 重复")
	}
	p := filepath.Join(t.TempDir(), "thread_history_1.sqlite")
	source, err := sql.Open("sqlite", p)
	check(t, err)
	defer source.Close()
	source.SetMaxOpenConns(1)
	_, err = source.Exec("PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0; CREATE TABLE thread_items(thread_id TEXT,item_id TEXT,rollout_ordinal INTEGER,created_at_ms INTEGER,item_json TEXT,updated_at_ordinal INTEGER);")
	check(t, err)
	_, err = source.Exec("INSERT INTO thread_items VALUES (?,?,?,?,?,?)", "canonical", "user-a", 1, 1767225600000, jsonText(user), 1)
	check(t, err)
	_, err = source.Exec("INSERT INTO thread_items VALUES (?,?,?,?,?,?)", "canonical", "agent-a", 3, 1767225600000, jsonText(agent), 3)
	check(t, err)
	before := map[string]string{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, e := os.ReadFile(p + suffix)
		if e == nil {
			before[suffix] = hash(b)
		}
	}
	dbEntry := rolloutFile{p, entry.source, false}
	check(t, i.history(dbEntry, metadata, false))
	if count(t, s, "messages") != 2 {
		t.Fatal("数据库投影与 JSONL 重复")
	}
	for suffix, digest := range before {
		b, e := os.ReadFile(p + suffix)
		check(t, e)
		if hash(b) != digest {
			t.Fatalf("原数据库 %s 被触碰", suffix)
		}
	}
	check(t, i.history(dbEntry, metadata, false))
	if i.status().Skipped != 1 {
		t.Fatal("未变化数据库没有跳过")
	}
	// A DB-only item survives without a rollout file.
	user["id"] = "user-b"
	_, err = source.Exec("INSERT INTO thread_items VALUES (?,?,?,?,?,?)", "db-only", "user-b", 0, 1767225600000, jsonText(user), 1)
	check(t, err)
	check(t, i.history(dbEntry, metadata, false))
	if count(t, s, "sessions") != 2 || count(t, s, "messages") != 3 {
		t.Fatal("仅存在数据库中的会话丢失")
	}
}

func TestLegacyMessagesRemainWhenStructuredPlanEventsAppear(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	entry := fixture(t, t.TempDir(), "mixed", responseEvent("user", "旧版本正文"), M{"type": "event_msg", "payload": M{"type": "item_completed", "item": M{"type": "Plan", "id": "plan-a", "text": "计划"}}}, responseEvent("assistant", "旧版本回答"))
	check(t, i.rollout(entry, map[string]M{}, false))
	page, e := s.messages(context.Background(), "mixed", url.Values{})
	check(t, e)
	if page["total"] != 2 {
		t.Fatal("结构化计划事件导致旧格式正文遗漏")
	}
	// Canonical completion may follow its response, and real repetitions remain separate.
	u := M{"type": "userMessage", "id": "u1", "content": []any{M{"type": "text", "text": "重复文本"}}}
	entry = fixture(t, t.TempDir(), "after", responseEvent("user", "重复文本"), M{"type": "event_msg", "payload": M{"type": "item_completed", "item": u}}, responseEvent("assistant", "回答"), responseEvent("user", "重复文本"), M{"type": "event_msg", "payload": M{"type": "item_completed", "item": M{"type": "userMessage", "id": "u2", "content": u["content"]}}})
	check(t, i.rollout(entry, map[string]M{}, false))
	page, e = s.messages(context.Background(), "after", url.Values{})
	check(t, e)
	if page["total"] != 3 {
		t.Fatalf("镜像去重丢失真实重复消息: %v", page["total"])
	}
}

func TestChatGPTZipBranchesAndIdempotence(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	conversation := M{"id": "cloud-one", "title": "我的 GPT 对话", "create_time": 1767225600, "update_time": 1767225601, "current_node": "answer-b", "mapping": M{"root": M{"parent": nil, "message": nil}, "question": M{"parent": "root", "message": M{"author": M{"role": "user"}, "create_time": 1767225600, "content": M{"parts": []any{"分支问题"}}}}, "answer-a": M{"parent": "question", "message": M{"author": M{"role": "assistant"}, "create_time": 1767225601, "content": M{"parts": []any{"旧回答"}}}}, "answer-b": M{"parent": "question", "message": M{"author": M{"role": "assistant"}, "create_time": 1767225602, "content": M{"parts": []any{"新回答"}}}}}}
	path := filepath.Join(t.TempDir(), "chatgpt.zip")
	f, e := os.Create(path)
	check(t, e)
	z := zip.NewWriter(f)
	w, e := z.Create("export/conversations.json")
	check(t, e)
	w.Write([]byte(jsonText([]any{conversation})))
	z.Close()
	f.Close()
	check(t, i.chatgpt(path, false))
	check(t, i.chatgpt(path, false))
	if count(t, s, "sessions") != 1 || count(t, s, "messages") != 3 {
		t.Fatal("分支丢失或重复导入")
	}
	page, e := s.messages(context.Background(), "chatgpt:cloud-one", url.Values{})
	check(t, e)
	branches := 0
	for _, m := range page["items"].([]M) {
		if obj(m["metadata"])["branch"] == true {
			branches++
		}
	}
	if branches != 1 {
		t.Fatal("非当前分支标记不正确")
	}
}

func TestSearchReindexPreservesBinaryAndMessages(t *testing.T) {
	s := testStore(t)
	check(t, s.session(s.Write, "s", M{"title": "图片会话"}))
	body := "可检索正文\n" + strings.Repeat("ABCDEFGH", 512) + "\n尾部中文"
	_, e := s.message(s.Write, "s", "m", 0, "user", body, now(), "message", "", M{})
	check(t, e)
	check(t, s.reindex(nil))
	if count(t, s, "messages") != 1 {
		t.Fatal("重建丢失正文")
	}
	got, e := s.body(1)
	check(t, e)
	if got != body {
		t.Fatal("重建修改完整正文")
	}
	for _, term := range []string{"正文", "尾部中文"} {
		v, e := s.sessions(context.Background(), url.Values{"q": {term}})
		check(t, e)
		if len(v["items"].([]M)) != 1 {
			t.Fatal("可读文本未索引")
		}
	}
	v, e := s.sessions(context.Background(), url.Values{"q": {"ABCDEFGH"}})
	check(t, e)
	if len(v["items"].([]M)) != 0 {
		t.Fatal("二进制仍污染索引")
	}
}

func TestHTTPReadOnlyBoundaryAndBackup(t *testing.T) {
	s := testStore(t)
	check(t, s.session(s.Write, "safe", M{"title": "安全正文"}))
	_, e := s.message(s.Write, "safe", "m", 0, "user", "只读历史", now(), "message", "", M{})
	check(t, e)
	app := &App{Store: s, Importer: newImporter(s)}
	req := httptest.NewRequest("POST", "http://localhost/api/sessions/safe/favorite", strings.NewReader(`{"favorite":true}`))
	res := httptest.NewRecorder()
	app.ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatal("无来源校验的写入被允许")
	}
	req = httptest.NewRequest("POST", "http://localhost/api/sessions/safe/favorite", strings.NewReader(`{"favorite":true}`))
	req.Header.Set("X-Echo-Request", "1")
	res = httptest.NewRecorder()
	app.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	req = httptest.NewRequest("GET", "http://evil.example/api/stats", nil)
	res = httptest.NewRecorder()
	app.ServeHTTP(res, req)
	if res.Code != 403 {
		t.Fatal("外部 Host 未拒绝")
	}
	req = httptest.NewRequest("GET", "http://localhost/api/backup", nil)
	res = httptest.NewRecorder()
	app.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	p := filepath.Join(t.TempDir(), "backup.sqlite")
	check(t, os.WriteFile(p, res.Body.Bytes(), 0600))
	backup, e := sql.Open("sqlite", p)
	check(t, e)
	defer backup.Close()
	var n int
	check(t, backup.QueryRow("SELECT count(*) FROM messages").Scan(&n))
	if n != 1 {
		t.Fatal("在线备份漏掉已提交 WAL")
	}
	req = httptest.NewRequest(http.MethodGet, "http://localhost/api/sessions/safe/export?format=md", nil)
	res = httptest.NewRecorder()
	app.ServeHTTP(res, req)
	text, _ := io.ReadAll(res.Result().Body)
	if res.Code != 200 || !strings.Contains(string(text), "只读历史") {
		t.Fatal("导出不可读")
	}
}
