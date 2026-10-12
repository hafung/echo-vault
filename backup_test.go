package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func backupRequest(t *testing.T, app *App, method, route, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost"+route, strings.NewReader(body))
	req.Header.Set("X-Echo-Request", "1")
	res := httptest.NewRecorder()
	app.ServeHTTP(res, req)
	return res
}

func awaitBackup(t *testing.T, app *App) backupJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res := backupRequest(t, app, "GET", "/api/backup/status", "")
		var status struct {
			Job backupJob `json:"job"`
		}
		check(t, json.Unmarshal(res.Body.Bytes(), &status))
		if status.Job.Stage == "ready" || status.Job.Stage == "error" {
			return status.Job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("备份任务未完成")
	return backupJob{}
}

func TestBackupJobsKeepAllDataAndDirectory(t *testing.T) {
	s := testStore(t)
	check(t, s.session(s.Write, "s", M{"title": "完整备份测试"}))
	_, err := s.message(s.Write, "s", "m", 0, "user", "正文测试", now(), "message", "", M{})
	check(t, err)
	_, err = s.Write.Exec("UPDATE sessions SET favorite=1 WHERE id='s'; INSERT INTO assets VALUES ('image','image/png',X'00010203'); INSERT INTO raw_blobs VALUES ('raw',X'040506')")
	check(t, err)
	app := &App{Store: s}
	t.Cleanup(app.backups.close)
	dir := filepath.Join(t.TempDir(), "备份目录 with spaces & ?")
	res := backupRequest(t, app, "POST", "/api/backup/settings", jsonText(M{"directory": dir}))
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	res = backupRequest(t, app, "POST", "/api/backup", `{"mode":"local"}`)
	if res.Code != 200 || !strings.Contains(res.Body.String(), `"stage":"copying"`) {
		t.Fatal(res.Body.String())
	}
	job := awaitBackup(t, app)
	if job.Stage != "ready" || job.DoneBytes != job.TotalBytes || job.TotalBytes == 0 {
		t.Fatalf("备份状态异常: %+v", job)
	}
	if filepath.Dir(job.Path) != dir {
		t.Fatal("未保存到指定位置")
	}
	verify := func(path string) {
		db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String()+"?mode=ro")
		check(t, err)
		defer db.Close()
		var integrity, body string
		var favorite int
		var payload []byte
		check(t, db.QueryRow("PRAGMA quick_check").Scan(&integrity))
		if integrity != "ok" {
			t.Fatal(integrity)
		}
		check(t, db.QueryRow("SELECT favorite FROM sessions WHERE id='s'").Scan(&favorite))
		if favorite != 1 {
			t.Fatal("收藏丢失")
		}
		check(t, db.QueryRow("SELECT payload FROM assets WHERE id='image'").Scan(&payload))
		if !bytes.Equal(payload, []byte{0, 1, 2, 3}) {
			t.Fatal("附件丢失")
		}
		check(t, db.QueryRow("SELECT payload FROM raw_blobs WHERE digest='raw'").Scan(&payload))
		if !bytes.Equal(payload, []byte{4, 5, 6}) {
			t.Fatal("原始数据丢失")
		}
		check(t, db.QueryRow("SELECT value FROM settings WHERE key='backup_directory'").Scan(&body))
		if body != dir {
			t.Fatal("备份位置未持久化")
		}
		var count int
		check(t, db.QueryRow(`SELECT count(*) FROM dialogue_long WHERE dialogue_long MATCH '"正文测"'`).Scan(&count))
		if count != 1 {
			t.Fatal("检索索引丢失")
		}
	}
	verify(job.Path)
	res = backupRequest(t, app, "POST", "/api/backup", `{"mode":"download"}`)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	download := awaitBackup(t, app)
	if download.Stage != "ready" {
		t.Fatal(download.Error)
	}

	if _, err := os.Stat(job.Path); err != nil {
		t.Fatal("新备份删除了本地历史", err)
	}
	res = backupRequest(t, app, "GET", "/api/backup/download?id="+download.ID, "")
	if res.Code != 200 || res.Body.Len() != int(download.TotalBytes) {
		t.Fatal("下载不完整")
	}
	path := filepath.Join(t.TempDir(), "download.sqlite")
	check(t, os.WriteFile(path, res.Body.Bytes(), 0600))
	verify(path)
	res = backupRequest(t, app, "GET", "/api/backup/download?id=unknown", "")
	if res.Code == 200 {
		t.Fatal("未知下载 ID 被接受")
	}
}

func TestBackupSnapshotStaysConsistentDuringWrites(t *testing.T) {
	s := testStore(t)
	check(t, s.session(s.Write, "before", M{"title": "快照开始前"}))
	// Force multiple backup steps without allocating a large Go byte slice.
	_, err := s.Write.Exec("INSERT INTO assets VALUES ('large','application/octet-stream',zeroblob(20000000))")
	check(t, err)
	written := false
	dest := filepath.Join(t.TempDir(), "snapshot.sqlite")
	err = s.backupTo(context.Background(), dest, func(done, total int64) {
		if !written {
			written = true
			check(t, s.session(s.Write, "after", M{"title": "复制期间新增"}))
		}
	})
	check(t, err)
	if !written {
		t.Fatal("未观察到备份进度")
	}
	db, err := sql.Open("sqlite", dest)
	check(t, err)
	defer db.Close()
	var n int
	check(t, db.QueryRow("SELECT count(*) FROM sessions").Scan(&n))
	if n != 1 || count(t, s, "sessions") != 2 {
		t.Fatal("备份未固定一致快照，或阻止了同步写入")
	}
}

func TestBackupJobFailureAndDuplicate(t *testing.T) {
	s := testStore(t)
	app := &App{Store: s}
	t.Cleanup(app.backups.close)
	app.backups.job = &backupJob{ID: "existing", Stage: "copying", Mode: "local"}
	res := backupRequest(t, app, "POST", "/api/backup", `{"mode":"download"}`)
	if !strings.Contains(res.Body.String(), `"id":"existing"`) {
		t.Fatal("重复点击启动了另一份备份")
	}
	app.backups.job = nil
	blocker := filepath.Join(t.TempDir(), "file")
	check(t, os.WriteFile(blocker, []byte("keep"), 0600))
	backupRequest(t, app, "POST", "/api/backup/settings", jsonText(M{"directory": filepath.Join(blocker, "nested")}))
	backupRequest(t, app, "POST", "/api/backup", `{"mode":"local"}`)
	job := awaitBackup(t, app)
	if job.Stage != "error" || job.Error == "" {
		t.Fatal("保存失败没有反馈")
	}
}

// Optional verification against a large existing archive, read-only throughout.
func TestLargeArchiveBackup(t *testing.T) {
	source := os.Getenv("ECHO_BACKUP_SOURCE")
	if source == "" {
		t.Skip("set ECHO_BACKUP_SOURCE for full-size verification")
	}
	uri := (&url.URL{Scheme: "file", Path: source}).String() + "?mode=ro&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", uri)
	check(t, err)
	defer db.Close()
	s := &Store{DB: db, Path: source}
	dest := filepath.Join(t.TempDir(), "full.sqlite")
	start := time.Now()
	check(t, s.backupTo(context.Background(), dest, nil))
	stat, err := os.Stat(dest)
	check(t, err)
	t.Logf("完整快照: %.2f GiB, %s", float64(stat.Size())/(1<<30), time.Since(start))
	copy, err := sql.Open("sqlite", dest)
	check(t, err)
	defer copy.Close()
	var integrity string
	check(t, copy.QueryRow("PRAGMA quick_check").Scan(&integrity))
	if integrity != "ok" {
		t.Fatal(integrity)
	}
	for _, table := range []string{"sessions", "messages", "assets", "raw_chunks", "raw_blobs", "settings"} {
		var before, after int64
		check(t, db.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&before))
		check(t, copy.QueryRow(fmt.Sprintf("SELECT count(*) FROM %s", table)).Scan(&after))
		if before != after {
			t.Fatalf("%s: %d != %d", table, before, after)
		}
		t.Logf("%s: %d 条完整保留", table, after)
	}
	if os.Getenv("ECHO_BACKUP_COMPRESS") == "1" {
		compressed := filepath.Join(filepath.Dir(dest), "full.sqlite.zst")
		check(t, os.WriteFile(compressed, nil, 0600))
		start = time.Now()
		check(t, compressSnapshot(context.Background(), dest, compressed, "zstd", nil))
		packed, err := os.Stat(compressed)
		check(t, err)
		t.Logf("Zstandard 快速压缩: %.2f GiB → %.2f GiB, 节省 %.1f%%, 压缩耗时 %s", float64(stat.Size())/(1<<30), float64(packed.Size())/(1<<30), 100*(1-float64(packed.Size())/float64(stat.Size())), time.Since(start))
		start = time.Now()
		restored := filepath.Join(filepath.Dir(dest), "restored.sqlite")
		check(t, restoreArchive(context.Background(), compressed, restored, nil))
		if !bytes.Equal(fileHash(t, dest), fileHash(t, restored)) {
			t.Fatal("完整档案恢复后字节不一致")
		}
		t.Logf("恢复、完整性检查及 SHA-256 字节一致校验: %s", time.Since(start))
	}
}
