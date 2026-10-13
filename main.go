package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed all:dist
var frontend embed.FS

type App struct {
	Store       *Store
	Importer    *Importer
	Static      fs.FS
	staticCache sync.Map
	backups     backupManager
	restores    restoreManager
	openLocal   func(context.Context, string) error
}
type staticAsset struct {
	data       []byte
	compressed []byte
	mime       string
}

func jsonResponse(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, errNotFound) {
		code = http.StatusNotFound
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(M{"error": err.Error()})
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			log.Printf("request failed %s %s: %v", r.Method, r.URL.Path, v)
			apiError(w, errors.New("处理失败，请查看本地服务日志"))
		}
	}()
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		http.Error(w, "仅允许本机访问", http.StatusForbidden)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https: http:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Cache-Control", "no-store")
	}
	if r.Method == http.MethodPost {
		origin := r.Header.Get("Origin")
		parsed, _ := url.Parse(origin)
		if r.Header.Get("X-Echo-Request") != "1" || (origin != "" && (parsed == nil || parsed.Host != r.Host)) {
			http.Error(w, "请求来源无效", http.StatusForbidden)
			return
		}
		if err := a.post(w, r); err != nil {
			apiError(w, err)
		}
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", 405)
		return
	}
	if err := a.get(w, r); err != nil {
		apiError(w, err)
	}
}
func (a *App) get(w http.ResponseWriter, r *http.Request) error {
	p := r.URL.Query()
	route := r.URL.Path
	switch route {
	case "/api/stats":
		v, e := a.Store.stats(r.Context(), p.Get("hidden") == "1")
		if e != nil {
			return e
		}
		jsonResponse(w, v)
		return nil
	case "/api/sessions":
		v, e := a.Store.sessions(r.Context(), p)
		if e != nil {
			return e
		}
		jsonResponse(w, v)
		return nil
	case "/api/sync":
		jsonResponse(w, a.Importer.status())
		return nil
	case "/api/health":
		jsonResponse(w, M{"app": "echo-vault"})
		return nil
	case "/api/sources":
		v, e := a.Importer.sources()
		if e != nil {
			return e
		}
		jsonResponse(w, M{"sources": v, "chatgpt": "自动收录 Windows 桌面应用 Chat 正文缓存；完整历史可通过官方 JSON / ZIP 导出补充。"})
		return nil
	case "/api/backup":
		return a.backup(w, r)
	case "/api/backup/status":
		return a.backupStatus(w)
	case "/api/backup/download":
		return a.downloadBackup(w, r)
	case "/api/restore/status":
		return a.restoreStatus(w)
	}
	parts := strings.Split(strings.Trim(route, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" {
		switch parts[1] {
		case "sessions":
			sid := parts[2]
			if len(parts) == 4 {
				switch parts[3] {
				case "hits":
					v, e := a.Store.hits(r.Context(), sid, p)
					if e != nil {
						return e
					}
					jsonResponse(w, v)
					return nil
				case "export":
					format := p.Get("format")
					if format == "" {
						format = "md"
					}
					suffix := ".md"
					mimeType := "text/markdown; charset=utf-8"
					switch format {
					case "json":
						suffix = ".json"
						mimeType = "application/json"
					case "raw":
						suffix = ".jsonl"
						mimeType = "application/x-ndjson"
					case "md":
					default:
						return errors.New("导出格式无效")
					}
					return a.export(w, r, sid, format, suffix, mimeType)
				default:
					return errNotFound
				}
			}
			v, e := a.Store.messages(r.Context(), sid, p)
			if e != nil {
				return e
			}
			jsonResponse(w, v)
			return nil
		case "messages":
			id, e := strconv.ParseInt(parts[2], 10, 64)
			if e != nil {
				return e
			}
			v, e := a.Store.body(id)
			if e != nil {
				return e
			}
			jsonResponse(w, M{"body": v})
			return nil
		case "assets":
			v, e := one(a.Store.DB, "SELECT mime,payload FROM assets WHERE id=?", parts[2])
			if e != nil {
				return e
			}
			data := v["payload"].([]byte)
			w.Header().Set("Content-Type", str(v["mime"]))
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, e = w.Write(data)
			return e
		}
	}
	if strings.HasPrefix(route, "/api/") {
		return errNotFound
	}
	return a.static(w, r)
}
func (a *App) post(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Path == "/api/restore/upload" {
		return a.startRestore(w, r, "")
	}
	if r.URL.Path == "/api/import" {
		return a.upload(w, r)
	}
	var v M
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil && err != io.EOF {
		return errors.New("请求 JSON 无效")
	}
	switch r.URL.Path {
	case "/api/local/open":
		return a.openLocalPath(w, r, v)
	case "/api/backup":
		return a.startBackup(w, v)
	case "/api/backup/settings":
		return a.saveBackupDirectory(w, v)
	case "/api/restore":
		if strings.TrimSpace(str(v["path"])) == "" {
			return errors.New("请填写备份文件路径")
		}
		return a.startRestore(w, r, str(v["path"]))
	case "/api/sync":
		jsonResponse(w, M{"started": a.Importer.startSync(v["full"] == true, v["automatic"] == true)})
		return nil
	case "/api/sources":
		if err := a.Importer.addSource(str(v["path"])); err != nil {
			return err
		}
		jsonResponse(w, M{"ok": true})
		return nil
	case "/api/projects/visibility":
		project := str(v["project"])
		if _, err := one(a.Store.DB, "SELECT id FROM sessions WHERE project=? LIMIT 1", project); err != nil {
			return err
		}
		var err error
		if v["hidden"] == true {
			_, err = a.Store.Write.Exec("INSERT OR IGNORE INTO hidden_projects(project) VALUES (?)", project)
		} else if v["hidden"] == false {
			_, err = a.Store.Write.Exec("DELETE FROM hidden_projects WHERE project=?", project)
		} else {
			return errors.New("请指定隐藏状态")
		}
		if err != nil {
			return err
		}
		jsonResponse(w, M{"ok": true})
		return nil
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "sessions" && parts[3] == "delete" {
		if v["confirmed"] != true {
			return errors.New("请确认删除此对话")
		}
		if err := a.Store.deleteSession(r.Context(), parts[2]); err != nil {
			return err
		}
		jsonResponse(w, M{"ok": true})
		return nil
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "sessions" && parts[3] == "visibility" {
		if _, err := one(a.Store.DB, "SELECT id FROM sessions WHERE id=?", parts[2]); err != nil {
			return err
		}
		hidden, ok := v["hidden"].(bool)
		if !ok {
			return errors.New("请指定隐藏状态")
		}
		_, err := a.Store.Write.Exec("INSERT INTO session_visibility(session_id,hidden) VALUES (?,?) ON CONFLICT(session_id) DO UPDATE SET hidden=excluded.hidden", parts[2], hidden)
		if err != nil {
			return err
		}
		jsonResponse(w, M{"ok": true})
		return nil
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "sessions" && parts[3] == "favorite" {
		favorite := 0
		if v["favorite"] == true {
			favorite = 1
		}
		result, err := a.Store.Write.Exec("UPDATE sessions SET favorite=? WHERE id=?", favorite, parts[2])
		if err != nil {
			return err
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			return errNotFound
		}
		jsonResponse(w, M{"ok": true})
		return nil
	}
	return errNotFound
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) error {
	const maxUpload = 512 * 1024 * 1024
	if r.ContentLength <= 0 || r.ContentLength > maxUpload {
		return errors.New("文件上限 512 MB；更大文件可通过路径添加来源")
	}
	name, _ := url.QueryUnescape(r.Header.Get("X-Filename"))
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".zip" && ext != ".json" {
		return errors.New("请选择 JSON 或 ZIP 文件")
	}
	folder := filepath.Join(filepath.Dir(a.Store.Path), "imports")
	if err := os.MkdirAll(folder, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(folder, "*.upload")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	written, err := io.Copy(f, r.Body)
	f.Close()
	if err != nil {
		return err
	}
	if written != r.ContentLength {
		return errors.New("上传中断，请重试")
	}
	data, err := os.Open(f.Name())
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, err = io.Copy(digest, data)
	data.Close()
	if err != nil {
		return err
	}
	dest := filepath.Join(folder, fmt.Sprintf("%x%s", digest.Sum(nil), ext))
	if !exists(dest) {
		if err = os.Rename(f.Name(), dest); err != nil {
			return err
		}
	}
	a.Importer.start(false)
	jsonResponse(w, M{"ok": true})
	return nil
}
func (a *App) export(w http.ResponseWriter, r *http.Request, sid, format, suffix, mimeType string) error {
	f, err := os.CreateTemp("", "echo-export-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = a.Store.export(sid, format, f); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="echo-conversation`+suffix+`"`)
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	_, err = io.Copy(w, f)
	return err
}
func (a *App) backup(w http.ResponseWriter, r *http.Request) error {
	dir, err := os.MkdirTemp("", "echo-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	dest := filepath.Join(dir, "echo.sqlite")
	err = a.Store.backupTo(r.Context(), dest, nil)
	if err != nil {
		return err
	}
	f, err := os.Open(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/vnd.sqlite3")
	w.Header().Set("Content-Disposition", `attachment; filename="echo-vault-`+time.Now().Format("20060102-150405")+`.sqlite"`)
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	_, err = io.Copy(w, f)
	return err
}
func (a *App) static(w http.ResponseWriter, r *http.Request) error {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	cached, ok := a.staticCache.Load(name)
	if !ok {
		data, err := fs.ReadFile(a.Static, name)
		if err != nil {
			if strings.Contains(filepath.Base(name), ".") {
				return errNotFound
			}
			name = "index.html"
			data, err = fs.ReadFile(a.Static, name)
			if err != nil {
				return err
			}
		}
		var compressed bytes.Buffer
		gz, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
		gz.Write(data)
		gz.Close()
		value := staticAsset{data, compressed.Bytes(), mime.TypeByExtension(filepath.Ext(name))}
		a.staticCache.Store(name, value)
		cached = value
	}
	asset := cached.(staticAsset)
	w.Header().Set("Content-Type", asset.mime)
	w.Header().Set("Vary", "Accept-Encoding")
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	data := asset.data
	if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.Header().Set("Content-Encoding", "gzip")
		data = asset.compressed
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, err := w.Write(data)
	return err
}

func main() {
	port := flag.Int("port", 8765, "本机端口")
	openBrowser := flag.Bool("open-browser", false, "启动后打开浏览器；已有服务时仅打开页面")
	data := flag.String("data", "data/echo.sqlite", "独立档案数据库路径")
	syncOnly := flag.Bool("sync-only", false, "同步后退出")
	full := flag.Bool("full", false, "重新读取全部来源")
	reindex := flag.Bool("reindex", false, "重建可读文本搜索索引后退出")
	compact := flag.Bool("compact", false, "压缩独立档案数据库后退出")
	deduplicate := flag.Bool("deduplicate", false, "按内容去重原始块，保留所有历史版本后退出")
	dialogue := flag.Bool("index-dialogue", false, "构建独立的正文检索索引后退出")
	restore := flag.String("restore", "", "从 SQLite 或 .sqlite.zst 完整备份恢复到 --data 指定的新文件，然后退出")
	flag.Parse()
	browserURL := fmt.Sprintf("http://localhost:%d", *port)
	maintenance := *syncOnly || *restore != "" || *reindex || *compact || *deduplicate || *dialogue
	if *openBrowser && !maintenance && echoVaultRunning(browserURL) {
		log.Printf("Echo Vault 已在运行，打开 %s；此启动窗口将退出", browserURL)
		if err := openArchiveBrowser(browserURL); err != nil {
			os.Exit(1)
		}
		return
	}
	if *restore != "" {
		if err := restoreArchive(context.Background(), localPath(*restore), localPath(*data), nil); err != nil {
			log.Fatal(err)
		}
		log.Printf("完整档案已恢复到 %s；使用 --data 指定此文件启动", *data)
		return
	}
	store, err := openStore(*data)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	if *dialogue {
		if err = store.indexDialogue(); err != nil {
			log.Fatal(err)
		}
		log.Print("正文检索索引已构建")
		return
	}
	if *deduplicate {
		last := 0
		err = store.deduplicate(func(n int) {
			if n-last >= 5000 {
				log.Printf("整理原始块 %d 条", n)
				last = n
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Print("原始块已去重，所有版本完整保留")
		return
	}
	if *reindex {
		last := 0
		err = store.reindex(func(n int) {
			if n-last >= 5000 {
				log.Printf("重建索引 %d 条", n)
				last = n
			}
		})
		if err != nil {
			log.Fatal(err)
		}
		log.Print("索引已重建，正文及原始事件完整保留")
		return
	}
	if *compact {
		if _, err = store.Write.Exec("PRAGMA wal_checkpoint(TRUNCATE); VACUUM; PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
			log.Fatal(err)
		}
		log.Print("档案存储已整理")
		return
	}
	importer := newImporter(store)
	if err = store.recoverChatGPTUI(); err != nil {
		log.Fatal("恢复 ChatGPT 界面数据: ", err)
	}
	if *syncOnly {
		importer.update(func(s *SyncStatus) {
			*s = SyncStatus{Running: true, Started: now(), Errors: []Issue{}, Warnings: []Issue{}}
		})
		importer.run(*full)
		json.NewEncoder(os.Stdout).Encode(importer.status())
		if len(importer.status().Errors) > 0 {
			os.Exit(1)
		}
		return
	}
	static, err := fs.Sub(frontend, "dist")
	if err != nil {
		log.Fatal(err)
	}
	app := &App{Store: store, Importer: importer, Static: static}
	defer app.backups.close()
	defer app.restores.close()
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", *port), Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		if *openBrowser && echoVaultRunning(browserURL) {
			if err := openArchiveBrowser(browserURL); err != nil {
				log.Fatal(err)
			}
			return
		}
		log.Fatal(err)
	}
	stopped := make(chan os.Signal, 1)
	signal.Notify(stopped, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stopped
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	log.Printf("回声 → http://localhost:%d", *port)
	if *openBrowser {
		go openArchiveBrowser(browserURL)
	}
	if err = server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
