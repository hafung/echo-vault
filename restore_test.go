package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func fileHash(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	check(t, err)
	defer f.Close()
	h := sha256.New()
	_, err = io.Copy(h, f)
	check(t, err)
	return h.Sum(nil)
}

func TestCompressedBackupRestoresExactDatabaseAndVisibility(t *testing.T) {
	s := testStore(t)
	check(t, s.session(s.Write, "s", M{"title": "完整压缩备份", "project": "project"}))
	_, err := s.message(s.Write, "s", "m", 0, "user", "全部正文", now(), "message", "", M{})
	check(t, err)
	_, err = s.Write.Exec("INSERT INTO assets VALUES ('image','image/png',zeroblob(1000000)); INSERT INTO raw_blobs VALUES ('raw',X'001234'); INSERT INTO hidden_projects VALUES ('project'); INSERT INTO session_visibility VALUES ('s',1); UPDATE sessions SET favorite=1")
	check(t, err)
	app := &App{Store: s}
	t.Cleanup(app.backups.close)
	dir := t.TempDir()
	if res := backupRequest(t, app, "POST", "/api/backup/settings", jsonText(M{"directory": dir})); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	for _, format := range []string{"zstd", "zstd-small"} {
		t.Run(format, func(t *testing.T) {
			res := backupRequest(t, app, "POST", "/api/backup", jsonText(M{"mode": "local", "format": format}))
			if res.Code != 200 {
				t.Fatal(res.Body.String())
			}
			job := awaitBackup(t, app)
			if job.Stage != "ready" || job.OutputBytes >= job.InputBytes || filepath.Ext(job.Path) != ".zst" {
				t.Fatalf("压缩失败: %+v", job)
			}
			dest := filepath.Join(t.TempDir(), "恢复.sqlite")
			check(t, restoreArchive(context.Background(), job.Path, dest, nil))
			db, err := sql.Open("sqlite", dest)
			check(t, err)
			defer db.Close()
			for query, want := range map[string]int{
				"SELECT count(*) FROM messages": 1, "SELECT length(payload) FROM assets WHERE id='image'": 1000000,
				"SELECT hidden FROM session_visibility WHERE session_id='s'": 1, "SELECT count(*) FROM hidden_projects": 1,
				"SELECT favorite FROM sessions WHERE id='s'": 1, "SELECT count(*) FROM raw_blobs": 1,
				`SELECT count(*) FROM dialogue_long WHERE dialogue_long MATCH '"全部正"'`: 1,
			} {
				var n int
				check(t, db.QueryRow(query).Scan(&n))
				if n != want {
					t.Fatalf("数据丢失: %s => %d", query, n)
				}
			}
		})
	}
	// Byte-for-byte round trip, rather than only checking selected table counts.
	snapshot := filepath.Join(dir, "source.sqlite")
	check(t, s.backupTo(context.Background(), snapshot, nil))
	compressed := filepath.Join(dir, "source.sqlite.zst")
	check(t, os.WriteFile(compressed, nil, 0600))
	check(t, compressSnapshot(context.Background(), snapshot, compressed, "zstd", nil))
	restored := filepath.Join(dir, "restored.sqlite")
	check(t, restoreArchive(context.Background(), compressed, restored, nil))
	if !bytes.Equal(fileHash(t, snapshot), fileHash(t, restored)) {
		t.Fatal("压缩恢复并非字节一致")
	}
	plain := filepath.Join(dir, "plain-restored.sqlite")
	check(t, restoreArchive(context.Background(), snapshot, plain, nil))
	if !bytes.Equal(fileHash(t, snapshot), fileHash(t, plain)) {
		t.Fatal("SQLite 恢复并非字节一致")
	}
	if err := restoreArchive(context.Background(), snapshot, plain, nil); err == nil {
		t.Fatal("覆盖了已有档案")
	}
	if !bytes.Equal(fileHash(t, snapshot), fileHash(t, plain)) {
		t.Fatal("已有档案被修改")
	}
	res := backupRequest(t, app, "POST", "/api/backup", `{"mode":"download","format":"zstd"}`)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	download := awaitBackup(t, app)
	if download.Stage != "ready" {
		t.Fatal(download.Error)
	}
	res = backupRequest(t, app, "GET", "/api/backup/download?id="+download.ID, "")
	if res.Code != 200 || res.Header().Get("Content-Type") != "application/zstd" || int64(res.Body.Len()) != download.OutputBytes {
		t.Fatal("压缩下载不完整", res.Header())
	}
	downloadPath := filepath.Join(dir, "download.sqlite.zst")
	check(t, os.WriteFile(downloadPath, res.Body.Bytes(), 0600))
	check(t, restoreArchive(context.Background(), downloadPath, filepath.Join(dir, "download-restored.sqlite"), nil))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := restoreArchive(ctx, compressed, filepath.Join(dir, "canceled.sqlite"), nil); err == nil {
		t.Fatal("取消失败")
	}
	broken, err := os.ReadFile(compressed)
	check(t, err)
	check(t, os.WriteFile(compressed, broken[:len(broken)-8], 0600))
	bad := filepath.Join(dir, "bad.sqlite")
	if err := restoreArchive(context.Background(), compressed, bad, nil); err == nil {
		t.Fatal("截断备份被接受")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("损坏备份被发布")
	}
	entries, err := filepath.Glob(filepath.Join(dir, "*.partial"))
	check(t, err)
	if len(entries) != 0 {
		t.Fatal("失败后残留临时文件", entries)
	}
}

func awaitRestore(t *testing.T, app *App) restoreJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var status struct {
			Job restoreJob `json:"job"`
		}
		res := backupRequest(t, app, "GET", "/api/restore/status", "")
		check(t, json.Unmarshal(res.Body.Bytes(), &status))
		if status.Job.Stage == "ready" || status.Job.Stage == "error" {
			return status.Job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("恢复任务未完成")
	return restoreJob{}
}

func TestRestorePathsUploadsAndInvalidArchives(t *testing.T) {
	s := testStore(t)
	check(t, s.session(s.Write, "keep", M{"title": "保留当前档案"}))
	dir := t.TempDir()
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	check(t, s.backupTo(context.Background(), snapshot, nil))
	app := &App{Store: s}
	t.Cleanup(app.restores.close)
	res := backupRequest(t, app, "POST", "/api/restore", jsonText(M{"path": snapshot}))
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	job := awaitRestore(t, app)
	if job.Stage != "ready" || job.Command == "" {
		t.Fatal(job)
	}
	if !bytes.Equal(fileHash(t, snapshot), fileHash(t, job.Path)) {
		t.Fatal("路径恢复不一致")
	}
	data, err := os.ReadFile(snapshot)
	check(t, err)
	req := httptest.NewRequest("POST", "http://localhost/api/restore/upload", bytes.NewReader(data))
	req.Header.Set("X-Echo-Request", "1")
	res = httptest.NewRecorder()
	app.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	job = awaitRestore(t, app)
	if job.Stage != "ready" {
		t.Fatal(job)
	}
	if count(t, s, "sessions") != 1 {
		t.Fatal("恢复改变了当前档案")
	}
	foreign := filepath.Join(dir, "foreign.sqlite")
	db, err := sql.Open("sqlite", foreign)
	check(t, err)
	_, err = db.Exec("CREATE TABLE example(id INTEGER)")
	check(t, err)
	check(t, db.Close())
	if err := restoreArchive(context.Background(), foreign, filepath.Join(dir, "foreign-copy.sqlite"), nil); err == nil {
		t.Fatal("接受了其他应用的数据库")
	}
	check(t, os.WriteFile(snapshot+"-wal", []byte("pending"), 0600))
	if err := restoreArchive(context.Background(), snapshot, filepath.Join(dir, "wal-copy.sqlite"), nil); err == nil {
		t.Fatal("可能遗漏 WAL 的文件被接受")
	}
	res = backupRequest(t, app, "POST", "/api/restore", jsonText(M{"path": filepath.Join(dir, "missing.sqlite")}))
	if res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if job := awaitRestore(t, app); job.Stage != "error" || job.Error == "" {
		t.Fatal("恢复失败没有反馈", job)
	}
}
