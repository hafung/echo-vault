package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
)

type backupJob struct {
	ID          string  `json:"id"`
	Mode        string  `json:"mode"`
	Stage       string  `json:"stage"`
	Started     float64 `json:"started"`
	Duration    float64 `json:"duration"`
	DoneBytes   int64   `json:"done_bytes"`
	TotalBytes  int64   `json:"total_bytes"`
	Path        string  `json:"path,omitempty"`
	Error       string  `json:"error,omitempty"`
	Format      string  `json:"format"`
	InputBytes  int64   `json:"input_bytes,omitempty"`
	OutputBytes int64   `json:"output_bytes,omitempty"`
	file        string
}

type backupManager struct {
	mu     sync.Mutex
	job    *backupJob
	cancel context.CancelFunc
	done   chan struct{}
	expiry *time.Timer
}

func (b *backupManager) close() {
	b.mu.Lock()
	if b.cancel != nil {
		b.cancel()
	}
	if b.expiry != nil {
		b.expiry.Stop()
	}
	done := b.done
	b.mu.Unlock()
	if done != nil {
		<-done
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.job != nil && b.job.Mode == "download" && b.job.file != "" {
		os.RemoveAll(filepath.Dir(b.job.file))
	}
}

// Keep a read snapshot throughout the backup so concurrent imports cannot
// repeatedly restart a large copy. Writers remain free to append to the WAL.
func (s *Store) backupTo(ctx context.Context, dest string, progress func(int64, int64)) error {
	conn, err := s.DB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var root, pageSize int64
	if err = tx.QueryRowContext(ctx, "SELECT rootpage FROM sqlite_schema LIMIT 1").Scan(&root); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, "PRAGMA page_size").Scan(&pageSize); err != nil {
		return err
	}
	return conn.Raw(func(raw any) (result error) {
		source, ok := raw.(interface {
			NewBackup(string) (*sqlite.Backup, error)
		})
		if !ok {
			return errors.New("SQLite 备份接口不可用")
		}
		uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(dest)}).String()
		backup, err := source.NewBackup(uri)
		if err != nil {
			return err
		}
		defer func() {
			if err := backup.Finish(); result == nil {
				result = err
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(2048)
			if err != nil {
				var sqliteErr *sqlite.Error
				if errors.As(err, &sqliteErr) && (sqliteErr.Code()&255 == 5 || sqliteErr.Code()&255 == 6) {
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(100 * time.Millisecond):
						continue
					}
				}
				return err
			}
			if progress != nil {
				progress(int64(backup.PageCount()-backup.Remaining())*pageSize, int64(backup.PageCount())*pageSize)
			}
			if !more {
				return nil
			}
		}
	})
}

func (a *App) backupDirectory() (string, error) {
	v, err := one(a.Store.DB, "SELECT value FROM settings WHERE key='backup_directory'")
	if errors.Is(err, errNotFound) {
		return filepath.Join(filepath.Dir(a.Store.Path), "backups"), nil
	}
	if err != nil {
		return "", err
	}
	return str(v["value"]), nil
}

func (a *App) saveBackupDirectory(w http.ResponseWriter, v M) error {
	path := strings.TrimSpace(str(v["directory"]))
	if path == "" {
		return errors.New("请填写备份目录")
	}
	dir := localPath(path)
	if stat, err := os.Stat(dir); err == nil && !stat.IsDir() {
		return errors.New("备份位置必须是目录")
	}
	if _, err := a.Store.Write.Exec("INSERT INTO settings(key,value) VALUES ('backup_directory',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", dir); err != nil {
		return err
	}
	jsonResponse(w, M{"directory": dir})
	return nil
}

func (a *App) backupStatus(w http.ResponseWriter) error {
	dir, err := a.backupDirectory()
	if err != nil {
		return err
	}
	a.backups.mu.Lock()
	defer a.backups.mu.Unlock()
	var job *backupJob
	if a.backups.job != nil {
		copy := *a.backups.job
		if backupActive(copy.Stage) {
			copy.Duration = now() - copy.Started
		}
		job = &copy
	}
	jsonResponse(w, M{"directory": dir, "job": job})
	return nil
}

func (a *App) startBackup(w http.ResponseWriter, v M) error {
	mode := str(v["mode"])
	if mode != "local" && mode != "download" {
		return errors.New("备份方式无效")
	}
	format := str(v["format"])
	if format == "" {
		format = "sqlite"
	}
	if format != "sqlite" && format != "zstd" && format != "zstd-small" {
		return errors.New("备份格式无效")
	}
	dir := ""
	if mode == "local" {
		var err error
		dir, err = a.backupDirectory()
		if err != nil {
			return err
		}
	}
	a.backups.mu.Lock()
	defer a.backups.mu.Unlock()
	if previous := a.backups.job; previous != nil {
		if backupActive(previous.Stage) {
			jsonResponse(w, previous)
			return nil
		}
		// Only retain one browser-download snapshot; local backups are permanent.
		if previous.Mode == "download" && previous.file != "" {
			os.RemoveAll(filepath.Dir(previous.file))
		}
	}
	if a.backups.expiry != nil {
		a.backups.expiry.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	done := make(chan struct{})
	a.backups.cancel = cancel
	a.backups.done = done
	job := &backupJob{ID: fmt.Sprintf("%d", time.Now().UnixNano()), Mode: mode, Format: format, Stage: "copying", Started: now()}
	a.backups.job = job
	jsonResponse(w, job)
	go func() { defer close(done); defer cancel(); a.runBackup(ctx, job.ID, mode, dir, format) }()
	return nil
}

func (a *App) updateBackup(id string, update func(*backupJob)) {
	a.backups.mu.Lock()
	defer a.backups.mu.Unlock()
	if job := a.backups.job; job != nil && job.ID == id {
		update(job)
	}
}

func backupActive(stage string) bool {
	return stage == "copying" || stage == "compressing" || stage == "finishing"
}

func (a *App) runBackup(ctx context.Context, id, mode, dir, format string) {
	err := a.createBackup(ctx, id, mode, dir, format)
	a.updateBackup(id, func(job *backupJob) {
		job.Duration = now() - job.Started
		if err != nil {
			job.Stage = "error"
			job.Error = err.Error()
		} else {
			job.Stage = "ready"
		}
	})
}

func (a *App) createBackup(ctx context.Context, id, mode, dir, format string) (result error) {
	if mode == "download" {
		var err error
		dir, err = os.MkdirTemp("", "echo-backup-")
		if err != nil {
			return err
		}
		defer func() {
			if result != nil {
				os.RemoveAll(dir)
			}
		}()
	} else if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Reserve a unique name and write beside the final file, avoiding a second
	// full copy even when the selected directory is on a different filesystem.
	f, err := os.CreateTemp(dir, ".echo-backup-*.partial")
	if err != nil {
		return err
	}
	partial := f.Name()
	if err = f.Close(); err != nil {
		os.Remove(partial)
		return err
	}
	defer os.Remove(partial)
	err = a.Store.backupTo(ctx, partial, func(done, total int64) {
		a.updateBackup(id, func(job *backupJob) { job.DoneBytes = done; job.TotalBytes = total })
	})
	if err != nil {
		return err
	}
	var inputBytes int64
	if stat, err := os.Stat(partial); err != nil {
		return err
	} else {
		inputBytes = stat.Size()
	}
	suffix := ".sqlite"
	if format != "sqlite" {
		compressed, err := os.CreateTemp(dir, ".echo-compressed-*.partial")
		if err != nil {
			return err
		}
		compressedPath := compressed.Name()
		if err = compressed.Close(); err != nil {
			os.Remove(compressedPath)
			return err
		}
		defer os.Remove(compressedPath)
		a.updateBackup(id, func(job *backupJob) {
			job.Stage = "compressing"
			job.DoneBytes = 0
			job.TotalBytes = inputBytes
			job.InputBytes = inputBytes
		})
		if err = compressSnapshot(ctx, partial, compressedPath, format, func(done int64) {
			a.updateBackup(id, func(job *backupJob) { job.DoneBytes = done })
		}); err != nil {
			return err
		}
		os.Remove(partial)
		partial = compressedPath
		suffix += ".zst"
	}
	a.updateBackup(id, func(job *backupJob) { job.Stage = "finishing" })
	f, err = os.OpenFile(partial, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	stat, err := f.Stat()
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	final := filepath.Join(dir, "gpt-vault-"+time.Now().Format("20060102-150405")+"-"+id+suffix)
	if err = os.Rename(partial, final); err != nil {
		return err
	}
	a.updateBackup(id, func(job *backupJob) {
		job.file = final
		job.DoneBytes = stat.Size()
		job.TotalBytes = stat.Size()
		job.InputBytes = inputBytes
		job.OutputBytes = stat.Size()
		if mode == "local" {
			job.Path = final
		}
	})
	if mode == "download" {
		a.backups.mu.Lock()
		a.backups.expiry = time.AfterFunc(time.Hour, func() {
			a.backups.mu.Lock()
			defer a.backups.mu.Unlock()
			os.RemoveAll(dir)
			if job := a.backups.job; job != nil && job.ID == id && job.Stage == "ready" {
				job.Stage = "expired"
				job.file = ""
			}
		})
		a.backups.mu.Unlock()
	}
	return nil
}

func (a *App) downloadBackup(w http.ResponseWriter, r *http.Request) error {
	a.backups.mu.Lock()
	job := a.backups.job
	if job == nil || job.ID != r.URL.Query().Get("id") || job.Mode != "download" || job.Stage != "ready" {
		a.backups.mu.Unlock()
		return errors.New("备份尚未就绪或已过期，请重新备份")
	}
	f, err := os.Open(job.file)
	a.backups.mu.Unlock()
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	contentType := "application/vnd.sqlite3"
	if strings.HasSuffix(f.Name(), ".zst") {
		contentType = "application/zstd"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(f.Name())+`"`)
	http.ServeContent(w, r, filepath.Base(f.Name()), stat.ModTime(), f)
	return nil
}
