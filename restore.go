package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"
)

type restoreJob struct {
	ID         string  `json:"id"`
	Stage      string  `json:"stage"`
	Started    float64 `json:"started"`
	Duration   float64 `json:"duration"`
	DoneBytes  int64   `json:"done_bytes"`
	TotalBytes int64   `json:"total_bytes"`
	Path       string  `json:"path,omitempty"`
	Command    string  `json:"command,omitempty"`
	Error      string  `json:"error,omitempty"`
}

type restoreManager struct {
	mu     sync.Mutex
	job    *restoreJob
	cancel context.CancelFunc
	done   chan struct{}
}

func restoreActive(stage string) bool {
	return stage == "uploading" || stage == "restoring" || stage == "validating"
}

func (m *restoreManager) close() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	done := m.done
	m.mu.Unlock()
	if done != nil {
		<-done
	}
}

func (a *App) updateRestore(id string, update func(*restoreJob)) {
	a.restores.mu.Lock()
	defer a.restores.mu.Unlock()
	if a.restores.job != nil && a.restores.job.ID == id {
		update(a.restores.job)
	}
}

func (a *App) restoreStatus(w http.ResponseWriter) error {
	a.restores.mu.Lock()
	defer a.restores.mu.Unlock()
	var job *restoreJob
	if a.restores.job != nil {
		copy := *a.restores.job
		if restoreActive(copy.Stage) {
			copy.Duration = now() - copy.Started
		}
		job = &copy
	}
	jsonResponse(w, M{"job": job})
	return nil
}

func (a *App) startRestore(w http.ResponseWriter, r *http.Request, path string) error {
	// Reserve before uploading to prevent concurrent multi-gigabyte uploads.
	a.restores.mu.Lock()
	if a.restores.job != nil && restoreActive(a.restores.job.Stage) {
		a.restores.mu.Unlock()
		return errors.New("已有恢复任务正在运行，请等待完成")
	}
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	job := &restoreJob{ID: id, Stage: "restoring", Started: now()}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	done := make(chan struct{})
	a.restores.job, a.restores.cancel, a.restores.done = job, cancel, done
	a.restores.mu.Unlock()
	started := false
	defer func() {
		if !started {
			cancel()
			close(done)
		}
	}()
	fail := func(err error) error {
		a.updateRestore(id, func(j *restoreJob) { j.Stage = "error"; j.Error = err.Error(); j.Duration = now() - j.Started })
		return err
	}
	folder := filepath.Join(filepath.Dir(a.Store.Path), "restored")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return fail(err)
	}
	removeSource := false
	if path == "" {
		if r.ContentLength <= 0 || r.ContentLength > 1<<40 {
			return fail(errors.New("请选择完整备份文件（最大 1 TB）；大文件建议使用本机路径"))
		}
		f, err := os.CreateTemp(folder, ".restore-upload-*.partial")
		if err != nil {
			return fail(err)
		}
		path = f.Name()
		a.updateRestore(id, func(j *restoreJob) { j.Stage = "uploading"; j.TotalBytes = r.ContentLength })
		n, err := io.CopyBuffer(f, &progressReader{ctx: r.Context(), r: http.MaxBytesReader(w, r.Body, 1<<40), progress: func(n int64) {
			a.updateRestore(id, func(j *restoreJob) { j.DoneBytes = n })
		}}, make([]byte, 1<<20))
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil && n != r.ContentLength {
			err = errors.New("上传中断，请重试")
		}
		if err != nil {
			os.Remove(path)
			return fail(err)
		}
		removeSource = true
	} else {
		path = localPath(path)
	}
	dest := filepath.Join(folder, "gpt-vault-restored-"+id+".sqlite")
	a.updateRestore(id, func(j *restoreJob) { j.Stage = "restoring"; j.DoneBytes = 0; j.TotalBytes = 0 })
	started = true
	go func() {
		defer close(done)
		defer cancel()
		if removeSource {
			defer os.Remove(path)
		}
		err := restoreArchive(ctx, path, dest, func(stage string, n, total int64) {
			a.updateRestore(id, func(j *restoreJob) { j.Stage = stage; j.DoneBytes = n; j.TotalBytes = total })
		})
		a.updateRestore(id, func(j *restoreJob) {
			j.Duration = now() - j.Started
			if err != nil {
				j.Stage = "error"
				j.Error = err.Error()
			} else {
				j.Stage = "ready"
				j.Path = dest
				j.Command = "./bin/gpt-vault --data '" + strings.ReplaceAll(dest, "'", "'\"'\"'") + "'"
			}
		})
	}()
	return a.restoreStatus(w)
}

// Restore to a new file only. Verification never opens the live archive for writing.
func restoreArchive(ctx context.Context, source, dest string, progress func(string, int64, int64)) (result error) {
	if _, err := os.Lstat(dest); err == nil {
		return errors.New("目标文件已存在，请选择新的数据路径")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	stat, err := in.Stat()
	if err != nil {
		return err
	}
	if !stat.Mode().IsRegular() {
		return errors.New("请选择完整备份文件")
	}
	reader := bufio.NewReader(in)
	header, err := reader.Peek(16)
	if err != nil {
		return errors.New("备份文件不完整")
	}
	compressed := bytes.Equal(header[:4], []byte{0x28, 0xb5, 0x2f, 0xfd})
	if !compressed && string(header) != "SQLite format 3\x00" {
		return errors.New("请选择 GPT Vault SQLite 或 Zstandard 压缩备份")
	}
	if !compressed {
		if wal, err := os.Stat(source + "-wal"); err == nil && wal.Size() > 0 {
			return errors.New("此数据库仍有 WAL，请先通过完整备份导出后恢复")
		}
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dest), ".echo-restore-*.partial")
	if err != nil {
		return err
	}
	partial := out.Name()
	defer os.Remove(partial)
	defer out.Close()
	counted := &progressReader{ctx: ctx, r: reader, progress: func(n int64) {
		if progress != nil {
			progress("restoring", n, stat.Size())
		}
	}}
	var data io.Reader = counted
	if compressed {
		decoder, err := zstd.NewReader(counted, zstd.WithDecoderConcurrency(2), zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(128<<20))
		if err != nil {
			return err
		}
		defer decoder.Close()
		data = decoder
	}
	n, err := io.CopyBuffer(out, io.LimitReader(data, (1<<40)+1), make([]byte, 1<<20))
	if err != nil {
		return fmt.Errorf("备份读取或解压失败: %w", err)
	}
	if n > 1<<40 {
		return errors.New("解压后的档案超过 1 TB")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	after, err := in.Stat()
	if err != nil {
		return err
	}
	if after.Size() != stat.Size() || !after.ModTime().Equal(stat.ModTime()) {
		return errors.New("读取期间备份文件发生变化，请重试")
	}
	if !compressed {
		if wal, err := os.Stat(source + "-wal"); err == nil && wal.Size() > 0 {
			return errors.New("读取期间数据库开始写入，请使用完整备份")
		}
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if progress != nil {
		progress("validating", stat.Size(), stat.Size())
	}
	if err = validateArchive(ctx, partial); err != nil {
		return err
	}
	// Link creates the destination exclusively, even if another process creates it
	// during validation. The temporary file lives on the same filesystem.
	return os.Link(partial, dest)
}

func validateArchive(ctx context.Context, path string) error {
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() + "?mode=ro&immutable=1"
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, table := range []string{"sessions", "messages", "files", "raw_chunks", "raw_blobs", "assets", "settings", "sync_runs", "db_items", "search_long", "search_short", "dialogue_long", "dialogue_short"} {
		var n int
		if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			return errors.New("不是完整的 GPT Vault 档案：缺少 " + table)
		}
	}
	for _, q := range []string{
		"SELECT id,title,project,cwd,platform,provider,model,created,updated,archived,favorite,parent_id,message_count,total_count,preview,metadata FROM sessions LIMIT 0",
		"SELECT id,session_id,identity,position,role,kind,phase,timestamp,body,body_hash,preview,metadata FROM messages LIMIT 0",
		"SELECT path,session_id,size,mtime,offset,ordinal,generation,head_hash,tail_hash,mode,last_sync,missing FROM files LIMIT 0",
		"SELECT id,path,session_id,generation,start,end,payload FROM raw_chunks LIMIT 0",
		"SELECT digest,payload FROM raw_blobs LIMIT 0", "SELECT id,mime,payload FROM assets LIMIT 0",
		"SELECT key,value FROM settings LIMIT 0", "SELECT id,started,finished,report FROM sync_runs LIMIT 0",
		"SELECT source,thread_id,item_id,marker FROM db_items LIMIT 0",
	} {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			return fmt.Errorf("GPT Vault 档案结构不兼容: %w", err)
		}
		if err = rows.Close(); err != nil {
			return err
		}
	}
	var integrity string
	if err = db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil {
		return err
	}
	if integrity != "ok" {
		return errors.New("备份完整性检查失败: " + integrity)
	}
	return nil
}
