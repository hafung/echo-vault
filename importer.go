package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

type Source struct {
	Path     string `json:"path"`
	Platform string `json:"platform"`
	Exists   bool   `json:"exists"`
	Kind     string `json:"kind"`
}
type Issue struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}
type SyncStatus struct {
	Running   bool    `json:"running"`
	Automatic bool    `json:"automatic"`
	Stage     string  `json:"stage"`
	Done      int     `json:"done"`
	Total     int     `json:"total"`
	Imported  int     `json:"imported"`
	Skipped   int     `json:"skipped"`
	Errors    []Issue `json:"errors"`
	Warnings  []Issue `json:"warnings"`
	File      string  `json:"file"`
	Started   float64 `json:"started"`
	Finished  float64 `json:"finished"`
	Duration  float64 `json:"duration"`
}
type Importer struct {
	Store *Store
	mu    sync.Mutex
	state SyncStatus
}
type rolloutFile struct {
	path     string
	source   Source
	archived bool
}

func newImporter(store *Store) *Importer {
	i := &Importer{Store: store, state: SyncStatus{Stage: "idle", Errors: []Issue{}, Warnings: []Issue{}}}
	if row, err := one(store.DB, "SELECT report FROM sync_runs ORDER BY id DESC LIMIT 1"); err == nil {
		json.Unmarshal([]byte(str(row["report"])), &i.state)
		i.state.Running = false
		if i.state.Errors == nil {
			i.state.Errors = []Issue{}
		}
		if i.state.Warnings == nil {
			i.state.Warnings = []Issue{}
		}
	}
	return i
}
func (i *Importer) status() SyncStatus {
	i.mu.Lock()
	defer i.mu.Unlock()
	s := i.state
	s.Errors = append([]Issue{}, s.Errors...)
	s.Warnings = append([]Issue{}, s.Warnings...)
	return s
}
func (i *Importer) update(fn func(*SyncStatus)) { i.mu.Lock(); defer i.mu.Unlock(); fn(&i.state) }
func (i *Importer) issue(path string, err error, warning bool) {
	i.update(func(s *SyncStatus) {
		v := Issue{path, err.Error()}
		if warning {
			s.Warnings = append(s.Warnings, v)
		} else {
			s.Errors = append(s.Errors, v)
		}
	})
}

const automaticSyncInterval = 5 * 60 // Seconds; shared by every open page.

func (i *Importer) start(full bool) bool { return i.startSync(full, false) }

func (i *Importer) startSync(full, automatic bool) bool {
	if !i.claimSync(automatic, now()) {
		return false
	}
	go i.run(full && !automatic)
	return true
}

func (i *Importer) claimSync(automatic bool, timestamp float64) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.state.Running {
		return false
	}
	if automatic {
		last := i.state.Finished
		if i.state.Started > last {
			last = i.state.Started
		}
		if last > 0 && timestamp-last < automaticSyncInterval {
			return false
		}
	}
	i.state = SyncStatus{Running: true, Automatic: automatic, Stage: "discover", Started: timestamp, Errors: []Issue{}, Warnings: []Issue{}}
	return true
}
func localPath(value string) string {
	value = strings.TrimSpace(value)
	home, _ := os.UserHomeDir()
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") {
		value = filepath.Join(home, value[2:])
	}
	if runtime.GOOS != "windows" && len(value) > 2 && value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		value = "/mnt/" + strings.ToLower(value[:1]) + "/" + strings.ReplaceAll(value[3:], "\\", "/")
	}
	p, err := filepath.Abs(value)
	if err != nil {
		return value
	}
	return filepath.Clean(p)
}
func projectName(cwd string) string {
	v := strings.TrimRight(strings.ReplaceAll(cwd, "\\", "/"), "/")
	for _, prefix := range []string{"/wwwroot/", "/projects/"} {
		if idx := strings.Index(strings.ToLower(v), prefix); idx >= 0 {
			return strings.Split(v[idx+len(prefix):], "/")[0]
		}
	}
	if v == "" {
		return "未分组"
	}
	return v[strings.LastIndex(v, "/")+1:]
}
func exists(path string) bool      { _, err := os.Stat(path); return err == nil }
func glob(pattern string) []string { v, _ := filepath.Glob(pattern); return v }
func (i *Importer) sources() ([]Source, error) {
	home, _ := os.UserHomeDir()
	candidates := []string{filepath.Join(home, ".codex")}
	if v := os.Getenv("CODEX_HOME"); v != "" {
		candidates = append(candidates, v)
	}
	if runtime.GOOS != "windows" {
		for _, pattern := range []string{"/home/*/.codex", "/root/.codex", "/mnt/*/Users/*/.codex", "/mnt/wsl/*/home/*/.codex"} {
			candidates = append(candidates, glob(pattern)...)
		}
	}
	candidates = append(candidates, desktopCachePaths()...)
	if row, err := one(i.Store.DB, "SELECT value FROM settings WHERE key='sources'"); err == nil {
		var configured []string
		if err = json.Unmarshal([]byte(str(row["value"])), &configured); err != nil {
			return nil, err
		}
		candidates = append(candidates, configured...)
	} else if !errors.Is(err, errNotFound) {
		return nil, err
	}
	sources := []Source{}
	seen := map[string]bool{}
	for _, p := range candidates {
		path := localPath(p)
		if seen[path] {
			continue
		}
		seen[path] = true
		platform := "WSL"
		if runtime.GOOS == "windows" || (strings.HasPrefix(path, "/mnt/") && !strings.HasPrefix(path, "/mnt/wsl/")) {
			platform = "Windows"
		}
		kind := "ChatGPT"
		if isDesktopCache(path) {
			kind = desktopCacheKind
		} else if stat, err := os.Stat(path); err == nil && stat.IsDir() && (filepath.Base(path) == ".codex" || exists(filepath.Join(path, "sessions")) || len(glob(filepath.Join(path, "state_*.sqlite"))) > 0) {
			kind = "Codex"
		}
		sources = append(sources, Source{path, platform, exists(path), kind})
	}
	return sources, nil
}
func (i *Importer) addSource(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("请输入来源路径")
	}
	path := localPath(value)
	if !exists(path) {
		return errors.New("路径不存在；支持 WSL 绝对路径或 C:\\… / D:\\… Windows 路径")
	}
	var values []string
	row, err := one(i.Store.DB, "SELECT value FROM settings WHERE key='sources'")
	if err == nil {
		if err = json.Unmarshal([]byte(str(row["value"])), &values); err != nil {
			return err
		}
	} else if !errors.Is(err, errNotFound) {
		return err
	}
	for _, v := range values {
		if v == path {
			return nil
		}
	}
	values = append(values, path)
	_, err = i.Store.Write.Exec("INSERT INTO settings VALUES ('sources',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", jsonText(values))
	return err
}
func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func fileSignature(paths ...string) string {
	var b strings.Builder
	for _, p := range paths {
		if stat, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%s:%d:%d;", p, stat.Size(), stat.ModTime().UnixNano())
		}
	}
	return b.String()
}
func snapshot(path string) (*sql.DB, func(), error) {
	// Originals are opened only as read-only files. SQLite operates on a private main+WAL copy.
	// Avoid mode=ro on original SQLite: it can still write SHM; immutable=1 would ignore WAL.
	for attempt := 0; attempt < 5; attempt++ {
		dir, err := os.MkdirTemp("", "echo-snapshot-")
		if err != nil {
			return nil, nil, err
		}
		before := fileSignature(path, path+"-wal")
		dest := filepath.Join(dir, filepath.Base(path))
		err = copyFile(path, dest)
		if err == nil && exists(path+"-wal") {
			err = copyFile(path+"-wal", dest+"-wal")
		}
		if err != nil {
			os.RemoveAll(dir)
			return nil, nil, err
		}
		if before != fileSignature(path, path+"-wal") {
			os.RemoveAll(dir)
			continue
		}
		db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: filepath.ToSlash(dest)}).String()+"?_pragma=query_only(1)")
		if err != nil {
			os.RemoveAll(dir)
			return nil, nil, err
		}
		db.SetMaxOpenConns(1)
		return db, func() { db.Close(); os.RemoveAll(dir) }, nil
	}
	return nil, nil, errors.New("数据库持续写入，未取得稳定副本；稍后再次同步即可")
}
func (i *Importer) run(full bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			i.issue("同步", fmt.Errorf("%v", recovered), false)
		}
		i.update(func(s *SyncStatus) {
			s.Running = false
			s.Finished = now()
			s.Duration = s.Finished - s.Started
			s.File = ""
			if len(s.Errors) > 0 {
				s.Stage = "done_with_errors"
			} else {
				s.Stage = "done"
			}
		})
		s := i.status()
		if _, err := i.Store.Write.Exec("INSERT INTO sync_runs(started,finished,report) VALUES (?,?,?)", s.Started, s.Finished, jsonText(s)); err != nil {
			i.issue("记录同步结果", err, false)
		}
	}()
	sources, err := i.sources()
	if err != nil {
		i.issue("发现来源", err, false)
		return
	}
	rollouts := []rolloutFile{}
	dbs := []rolloutFile{}
	caches := []Source{}
	exports := []string{}
	metadata := map[string]M{}
	for _, source := range sources {
		root := source.Path
		if !source.Exists {
			i.issue(root, errors.New("来源暂不可用，已有历史继续保留"), true)
			continue
		}
		if source.Kind == desktopCacheKind {
			caches = append(caches, source)
		} else if source.Kind == "Codex" {
			states := glob(filepath.Join(root, "state_*.sqlite"))
			sort.Sort(sort.Reverse(sort.StringSlice(states)))
			if len(states) > 0 {
				db, cleanup, e := snapshot(states[0])
				if e != nil {
					i.issue(states[0], e, true)
				} else {
					rows, e := query(db, "SELECT * FROM threads")
					if e != nil {
						i.issue(states[0], e, true)
					} else {
						for _, m := range rows {
							metadata[str(m["id"])] = m
						}
					}
					cleanup()
				}
			}
			if f, e := os.Open(filepath.Join(root, "session_index.jsonl")); e == nil {
				scanner := bufio.NewScanner(f)
				scanner.Buffer(make([]byte, 4096), 4*1024*1024)
				for scanner.Scan() {
					var d M
					if json.Unmarshal(scanner.Bytes(), &d) == nil {
						sid := str(d["id"])
						title := str(d["thread_name"])
						if title == "" {
							title = str(d["title"])
						}
						if title != "" {
							if metadata[sid] == nil {
								metadata[sid] = M{}
							}
							metadata[sid]["title"] = title
						}
					}
				}
				if e = scanner.Err(); e != nil {
					i.issue(f.Name(), e, true)
				}
				f.Close()
			}
			for _, folder := range []string{"sessions", "archived_sessions"} {
				filepath.WalkDir(filepath.Join(root, folder), func(p string, d os.DirEntry, e error) error {
					if e != nil {
						if !os.IsNotExist(e) {
							i.issue(p, e, true)
						}
						return nil
					}
					if !d.IsDir() && strings.HasSuffix(d.Name(), ".jsonl") {
						rollouts = append(rollouts, rolloutFile{p, source, folder == "archived_sessions"})
					}
					return nil
				})
			}
			for _, p := range glob(filepath.Join(root, "thread_history*.sqlite")) {
				dbs = append(dbs, rolloutFile{p, source, false})
			}
		} else if stat, e := os.Stat(root); e == nil && stat.IsDir() {
			filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
				if e != nil {
					return nil
				}
				if !d.IsDir() && ((strings.HasPrefix(d.Name(), "conversations") && strings.HasSuffix(d.Name(), ".json")) || strings.HasSuffix(d.Name(), ".zip")) {
					exports = append(exports, p)
				}
				return nil
			})
		} else {
			exports = append(exports, root)
		}
	}
	inbox := filepath.Join(filepath.Dir(i.Store.Path), "imports")
	exports = append(exports, glob(filepath.Join(inbox, "*.json"))...)
	exports = append(exports, glob(filepath.Join(inbox, "*.zip"))...)
	home, _ := os.UserHomeDir()
	downloads := []string{filepath.Join(home, "Downloads")}
	downloads = append(downloads, glob("/mnt/*/Users/*/Downloads")...)
	for _, d := range downloads {
		exports = append(exports, glob(filepath.Join(d, "conversations*.json"))...)
		exports = append(exports, glob(filepath.Join(d, "*chatgpt*.zip"))...)
	}
	unique := map[string]bool{}
	dedup := []string{}
	for _, p := range exports {
		if !unique[p] {
			unique[p] = true
			dedup = append(dedup, p)
		}
	}
	exports = dedup
	sort.Slice(rollouts, func(a, b int) bool { return rollouts[a].path < rollouts[b].path })
	sort.Strings(exports)
	i.update(func(s *SyncStatus) {
		s.Total = len(rollouts) + len(dbs) + len(exports) + len(caches)
		s.Stage = "rollouts"
	})
	if files, e := query(i.Store.DB, "SELECT path FROM files"); e == nil {
		for _, f := range files {
			p := str(f["path"])
			base := strings.Split(p, "::")[0]
			missing := 0
			if !exists(base) {
				missing = 1
			}
			if _, e = i.Store.Write.Exec("UPDATE files SET missing=? WHERE path=?", missing, p); e != nil {
				i.issue(p, e, true)
			}
		}
	}
	for _, entry := range rollouts {
		i.update(func(s *SyncStatus) { s.File = filepath.Base(entry.path) })
		if e := i.rollout(entry, metadata, full); e != nil {
			i.issue(entry.path, e, false)
		}
		i.update(func(s *SyncStatus) { s.Done++ })
	}
	i.update(func(s *SyncStatus) { s.Stage = "database" })
	for _, entry := range dbs {
		i.update(func(s *SyncStatus) { s.File = filepath.Base(entry.path) })
		if e := i.history(entry, metadata, full); e != nil {
			i.issue(entry.path, e, false)
		}
		i.update(func(s *SyncStatus) { s.Done++ })
	}
	i.update(func(s *SyncStatus) { s.Stage = "chatgpt" })
	for _, p := range exports {
		i.update(func(s *SyncStatus) { s.File = filepath.Base(p) })
		if e := i.chatgpt(p, full); e != nil {
			i.issue(p, e, false)
		}
		i.update(func(s *SyncStatus) { s.Done++ })
	}
	i.update(func(s *SyncStatus) { s.Stage = "chatgpt_desktop" })
	for _, source := range caches {
		i.update(func(s *SyncStatus) { s.File = "ChatGPT Desktop 缓存" })
		if e := i.desktopChatGPT(source, full); e != nil {
			i.issue(source.Path, e, false)
		}
		i.update(func(s *SyncStatus) { s.Done++ })
	}
	if _, e := i.Store.Write.Exec("PRAGMA optimize"); e != nil {
		i.issue("索引维护", e, true)
	}
}
func first(v ...any) any {
	for _, x := range v {
		if x == nil {
			continue
		}
		switch t := x.(type) {
		case string:
			if t == "" || t == "0" {
				continue
			}
		case int64:
			if t == 0 {
				continue
			}
		case float64:
			if t == 0 {
				continue
			}
		case int:
			if t == 0 {
				continue
			}
		}
		return x
	}
	return nil
}
func (i *Importer) prepareSession(db DBQuery, sid string, meta M, source Source, archived bool) error {
	cwd := str(meta["cwd"])
	created := stamp(first(meta["created_at_ms"], meta["created_at"], meta["timestamp"]))
	if created == 0 {
		created = now()
	}
	title := str(first(meta["title"], meta["first_user_message"]))
	if title == "" {
		title = "未命名对话"
	}
	parent := first(meta["parent_thread_id"], obj(obj(obj(meta["source"])["subagent"])["thread_spawn"])["parent_thread_id"])
	keep := M{}
	for _, k := range []string{"git", "model_provider", "source", "cli_version", "originator", "history_mode", "agent_nickname"} {
		if meta[k] != nil {
			keep[k] = meta[k]
		}
	}
	archive := 0
	if archived || integer(meta["archived"]) == 1 || meta["archived"] == true {
		archive = 1
	}
	return i.Store.session(db, sid, M{"title": clip(title, 240), "project": projectName(cwd), "cwd": cwd, "platform": source.Platform, "created": created, "updated": stamp(first(meta["updated_at_ms"], meta["updated_at"])), "model": str(meta["model"]), "archived": archive, "parent_id": parent, "metadata": jsonText(keep)})
}
func textContent(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case []any:
		parts := []string{}
		for _, p := range v {
			if t := textContent(p); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"text", "output_text", "input_text"} {
			if t, ok := v[key].(string); ok {
				return t
			}
		}
	}
	return ""
}
func (i *Importer) assets(db DBQuery, parts any, paths []any) ([]any, error) {
	result := []any{}
	for _, part := range array(parts) {
		p := obj(part)
		v := first(p["image_url"], p["url"], p["asset_pointer"])
		if m, ok := v.(map[string]any); ok {
			v = m["url"]
		}
		u := str(v)
		if strings.HasPrefix(u, "data:image/") {
			prefix, data, ok := strings.Cut(u, ",")
			if !ok {
				continue
			}
			if len(data) > 45*1024*1024 {
				continue
			}
			raw, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				continue
			}
			mimeType := strings.Split(strings.TrimPrefix(prefix, "data:"), ";")[0]
			aid := hash(raw)
			if _, err = db.Exec("INSERT OR IGNORE INTO assets VALUES (?,?,?)", aid, mimeType, raw); err != nil {
				return nil, err
			}
			result = append(result, aid)
		} else if u != "" {
			result = append(result, M{"reference": u})
		}
		if str(p["type"]) == "localImage" || str(p["type"]) == "local_image" || str(p["type"]) == "image" {
			if p["path"] != nil {
				paths = append(paths, p["path"])
			}
		}
	}
	for _, v := range paths {
		p := localPath(str(v))
		ext := strings.ToLower(filepath.Ext(p))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" && ext != ".webp" {
			continue
		}
		stat, e := os.Stat(p)
		if e != nil {
			result = append(result, M{"reference": str(v)})
			continue
		}
		if stat.Size() > 32*1024*1024 {
			result = append(result, M{"reference": str(v)})
			continue
		}
		raw, e := os.ReadFile(p)
		if e != nil {
			result = append(result, M{"reference": str(v)})
			continue
		}
		aid := hash(raw)
		if _, e = db.Exec("INSERT OR IGNORE INTO assets VALUES (?,?,?)", aid, mime.TypeByExtension(ext), raw); e != nil {
			return nil, e
		}
		result = append(result, aid)
	}
	return result, nil
}
func (i *Importer) item(db DBQuery, sid string, item M, ordinal int64, ts float64, identity string) (bool, error) {
	typ := str(item["type"])
	role := "tool"
	body := ""
	meta := M{"item_type": typ}
	var err error
	switch typ {
	case "userMessage", "user_message":
		role = "user"
		body = textContent(first(item["content"], item["message"]))
		meta["assets"], err = i.assets(db, item["content"], array(item["local_images"]))
	case "agentMessage", "agent_message":
		role = "assistant"
		body = str(first(item["text"], item["message"]))
	case "reasoning":
		role = "reasoning"
		body = textContent(item["summary"]) + "\n" + textContent(item["content"])
	case "commandExecution":
		body = str(item["command"]) + "\n\n" + str(item["aggregatedOutput"])
	case "fileChange":
		b, _ := json.MarshalIndent(first(item["changes"], item), "", "  ")
		body = string(b)
	case "contextCompaction":
		role = "context"
		body = str(first(item["text"], item["message"], "对话上下文已压缩"))
	case "imageGeneration", "imageView":
		body = str(first(item["revisedPrompt"], item["prompt"], item["path"], "图片"))
		paths := []any{}
		for _, k := range []string{"savedPath", "path"} {
			if item[k] != nil {
				paths = append(paths, item[k])
			}
		}
		meta["assets"], err = i.assets(db, nil, paths)
	default:
		b, _ := json.MarshalIndent(item, "", "  ")
		body = string(b)
	}
	if err != nil {
		return false, err
	}
	if identity == "" {
		identity = "item:" + str(first(item["id"], ordinal))
	}
	// Some versions emit the response before item_completed, others after it.
	// Give a response already recorded at the same event its stable canonical ID.
	if role == "user" || role == "assistant" {
		if _, e := one(db, "SELECT id FROM messages WHERE session_id=? AND identity=?", sid, identity); errors.Is(e, errNotFound) {
			if twin, e := one(db, "SELECT id FROM messages WHERE session_id=? AND role=? AND body_hash=? AND identity LIKE 'response:%' AND position BETWEEN ? AND ? ORDER BY abs(position-?) LIMIT 1", sid, role, hash([]byte(body)), ordinal-2, ordinal+2, ordinal); e == nil {
				if _, e = db.Exec("UPDATE messages SET identity=? WHERE id=?", identity, twin["id"]); e != nil {
					return false, e
				}
			}
		}
	}
	return i.Store.message(db, sid, identity, ordinal, role, body, ts, typ, str(item["phase"]), meta)
}
func (i *Importer) response(db DBQuery, sid string, p M, ordinal int64, ts float64) (bool, error) {
	typ := str(p["type"])
	role := "tool"
	body := ""
	meta := M{}
	var err error
	switch typ {
	case "message":
		role = str(p["role"])
		body = textContent(p["content"])
		if role != "user" && role != "assistant" {
			role = "context"
		}
		trim := strings.TrimSpace(body)
		if strings.HasPrefix(trim, "# AGENTS.md instructions") || strings.HasPrefix(trim, "<environment_context>") {
			role = "context"
		}
		meta["assets"], err = i.assets(db, p["content"], nil)
	case "reasoning":
		role = "reasoning"
		body = textContent(p["summary"]) + "\n" + textContent(p["content"])
	case "function_call", "custom_tool_call":
		body = str(first(p["name"], "工具")) + "\n" + str(first(p["arguments"], p["input"], ""))
	case "function_call_output", "custom_tool_call_output":
		body = textContent(p["output"])
		if body == "" && p["output"] != nil {
			body = jsonText(p["output"])
		}
	case "image_generation_call":
		body = str(first(p["revised_prompt"], "图片生成"))
		paths := []any{}
		if p["saved_path"] != nil {
			paths = append(paths, p["saved_path"])
		}
		meta["assets"], err = i.assets(db, nil, paths)
	default:
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if role == "user" || role == "assistant" {
		if _, e := one(db, "SELECT id FROM messages WHERE session_id=? AND role=? AND body_hash=? AND identity LIKE 'item:%' AND position BETWEEN ? AND ? LIMIT 1", sid, role, hash([]byte(body)), ordinal-2, ordinal+2); e == nil {
			return false, nil
		} else if !errors.Is(e, errNotFound) {
			return false, e
		}
	}
	if call := str(p["call_id"]); call != "" {
		if _, e := one(db, "SELECT id FROM messages WHERE session_id=? AND identity=?", sid, "item:"+call); e == nil {
			return false, nil
		}
	}
	identity := fmt.Sprintf("response:%d:%s", ordinal, hash([]byte(body))[:20])
	return i.Store.message(db, sid, identity, ordinal, role, body, ts, typ, str(p["phase"]), meta)
}
func (i *Importer) rollout(entry rolloutFile, metadata map[string]M, full bool) error {
	i.Store.importMu.Lock()
	defer i.Store.importMu.Unlock()
	path := entry.path
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	old, err := one(i.Store.DB, "SELECT * FROM files WHERE path=?", path)
	if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	if old != nil && !full && integer(old["size"]) == stat.Size() && integer(old["mtime"]) == stat.ModTime().UnixNano() {
		i.update(func(s *SyncStatus) { s.Skipped++ })
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := bufio.NewReaderSize(f, 256*1024)
	header, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var event struct {
		Payload M `json:"payload"`
	}
	if err = json.Unmarshal(header, &event); err != nil {
		return fmt.Errorf("会话首行无法识别: %w", err)
	}
	meta := event.Payload
	sid := str(meta["id"])
	if sid == "" {
		sid = hash([]byte(path))
	}
	combined := M{}
	for k, v := range meta {
		combined[k] = v
	}
	for k, v := range metadata[sid] {
		combined[k] = v
	}
	probe := make([]byte, 192*1024)
	n, _ := f.ReadAt(probe, 0)
	probe = probe[:n]
	mode := "events"
	if bytes.Contains(probe, []byte(`"response_item"`)) {
		mode = "hybrid"
	}
	if bytes.Contains(probe, []byte(`"item_completed"`)) || str(combined["history_mode"]) == "paginated" {
		mode = "hybrid"
	}
	offset, ordinal, generation := int64(0), int64(0), int64(0)
	if old != nil {
		generation = integer(old["generation"])
		previousOffset := integer(old["offset"])
		valid := false
		if previousOffset > 0 && stat.Size() >= previousOffset {
			head := make([]byte, min(previousOffset, 1024))
			f.ReadAt(head, 0)
			tail := make([]byte, min(previousOffset, 512))
			f.ReadAt(tail, previousOffset-int64(len(tail)))
			valid = hash(head) == str(old["head_hash"]) && hash(tail) == str(old["tail_hash"])
		}
		if valid && stat.Size() > previousOffset && !full {
			offset = previousOffset
			ordinal = integer(old["ordinal"])
			if str(old["mode"]) != "events" {
				mode = "hybrid"
			}
		} else if !valid || (full && (stat.Size() != integer(old["size"]) || stat.ModTime().UnixNano() != integer(old["mtime"]))) || (stat.Size() == integer(old["size"]) && stat.ModTime().UnixNano() != integer(old["mtime"])) {
			generation++
		}
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return err
	}
	reader.Reset(f)
	tx, err := i.Store.Write.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	allowed, err := importSession(tx, sid, full)
	if err != nil {
		return err
	}
	if !allowed {
		i.update(func(s *SyncStatus) { s.Skipped++ })
		return nil
	}
	if err = i.prepareSession(tx, sid, combined, entry.source, entry.archived); err != nil {
		return err
	}
	added := 0
	firstUser := ""
	var chunk bytes.Buffer
	chunkStart := offset
	lines := 0
	flush := func() error {
		if chunk.Len() == 0 {
			return nil
		}
		if e := i.checkpoint(tx, f, path, stat, sid, generation, chunkStart, offset, ordinal, mode, chunk.Bytes()); e != nil {
			return e
		}
		if e := tx.Commit(); e != nil {
			return e
		}
		var e error
		tx, e = i.Store.Write.Begin()
		chunk.Reset()
		chunkStart = offset
		lines = 0
		return e
	}
	for offset < stat.Size() {
		line, e := reader.ReadBytes('\n')
		if e == io.EOF || len(line) == 0 || line[len(line)-1] != '\n' || offset+int64(len(line)) > stat.Size() {
			break
		}
		if e != nil {
			return e
		}
		chunk.Write(line)
		offset += int64(len(line))
		lines++
		var d struct {
			Timestamp any             `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if e = json.Unmarshal(line, &d); e != nil {
			i.issue(path, fmt.Errorf("第 %d 行 JSON 损坏，原始字节已保留", ordinal+1), true)
		} else {
			var p M
			json.Unmarshal(d.Payload, &p)
			changed := false
			ts := stamp(d.Timestamp)
			if d.Type == "turn_context" && str(p["model"]) != "" {
				if _, e = tx.Exec("UPDATE sessions SET model=? WHERE id=?", p["model"], sid); e != nil {
					return e
				}
			}
			if d.Type == "event_msg" && str(p["type"]) == "item_completed" {
				item := obj(p["item"])
				changed, e = i.item(tx, sid, item, ordinal, ts, "")
				if str(item["type"]) == "userMessage" && firstUser == "" {
					firstUser = textContent(item["content"])
				}
			}
			if d.Type == "response_item" {
				changed, e = i.response(tx, sid, p, ordinal, ts)
				if str(p["role"]) == "user" && firstUser == "" {
					t := textContent(p["content"])
					if !strings.HasPrefix(strings.TrimSpace(t), "# AGENTS.md") && !strings.HasPrefix(strings.TrimSpace(t), "<environment_context>") {
						firstUser = t
					}
				}
			}
			if d.Type == "event_msg" && mode == "events" && (str(p["type"]) == "user_message" || str(p["type"]) == "agent_message") {
				p["content"] = p["message"]
				p["id"] = strconvString(ordinal)
				changed, e = i.item(tx, sid, p, ordinal, ts, "")
			}
			if e != nil {
				return e
			}
			if changed {
				added++
			}
		}
		ordinal++
		if lines >= 256 || chunk.Len() >= 8*1024*1024 {
			if e = flush(); e != nil {
				return e
			}
		}
	}
	if err = flush(); err != nil {
		return err
	}
	if firstUser != "" && str(combined["title"]) == "" {
		if _, err = tx.Exec("UPDATE sessions SET title=? WHERE id=? AND title='未命名对话'", clip(whitespace.ReplaceAllString(firstUser, " "), 140), sid); err != nil {
			return err
		}
	}
	if err = i.Store.recount(tx, sid); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	tx = nil
	i.update(func(s *SyncStatus) { s.Imported += added })
	return nil
}
func strconvString(v int64) string { return fmt.Sprint(v) }
func (i *Importer) checkpoint(db DBQuery, f *os.File, path string, stat os.FileInfo, sid string, generation, start, end, ordinal int64, mode string, raw []byte) error {
	if err := i.Store.addRaw(db, path, sid, generation, start, end, raw); err != nil {
		return err
	}

	head := make([]byte, min(end, 1024))
	f.ReadAt(head, 0)
	tail := make([]byte, min(end, 512))
	f.ReadAt(tail, end-int64(len(tail)))
	_, err := db.Exec(`INSERT INTO files(path,session_id,size,mtime,offset,ordinal,generation,head_hash,tail_hash,mode,last_sync) VALUES (?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET session_id=excluded.session_id,size=excluded.size,mtime=excluded.mtime,offset=excluded.offset,ordinal=excluded.ordinal,generation=excluded.generation,head_hash=excluded.head_hash,tail_hash=excluded.tail_hash,mode=excluded.mode,last_sync=excluded.last_sync,missing=0`, path, sid, end, stat.ModTime().UnixNano(), end, ordinal, generation, hash(head), hash(tail), mode, now())
	if err != nil {
		return err
	}
	return i.Store.recount(db, sid)
}
func (i *Importer) history(entry rolloutFile, metadata map[string]M, full bool) error {
	i.Store.importMu.Lock()
	defer i.Store.importMu.Unlock()
	signature := fileSignature(entry.path, entry.path+"-wal")
	key := "db_signature:" + entry.path
	if !full {
		if previous, e := one(i.Store.DB, "SELECT value FROM settings WHERE key=?", key); e == nil && str(previous["value"]) == signature {
			i.update(func(s *SyncStatus) { s.Skipped++ })
			return nil
		}
	}
	src, cleanup, err := snapshot(entry.path)
	if err != nil {
		return err
	}
	defer cleanup()
	columns, err := query(src, "PRAGMA table_info(thread_items)")
	if err != nil {
		return err
	}
	fields := []string{}
	for _, column := range columns {
		name := str(column["name"])
		if name != "item_json" {
			fields = append(fields, `"`+strings.ReplaceAll(name, `"`, `""`)+`"`)
		}
	}
	// Poll small SDK watermarks first. Unchanged large JSON bodies and images are never read again.
	rows, err := query(src, "SELECT "+strings.Join(fields, ",")+" FROM thread_items ORDER BY thread_id,rollout_ordinal")
	if err != nil {
		return err
	}
	tx, err := i.Store.Write.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if tx != nil {
			tx.Rollback()
		}
	}()
	touched := map[string]bool{}
	ignored := map[string]bool{}
	added := 0
	for recordIndex, d := range rows {
		if recordIndex > 0 && recordIndex%256 == 0 {
			if err = tx.Commit(); err != nil {
				return err
			}
			tx, err = i.Store.Write.Begin()
			if err != nil {
				return err
			}
		}
		sid := str(d["thread_id"])
		if ignored[sid] {
			continue
		}
		if !touched[sid] {
			allowed, e := importSession(tx, sid, full)
			if e != nil {
				return e
			}
			if !allowed {
				ignored[sid] = true
				i.update(func(s *SyncStatus) { s.Skipped++ })
				continue
			}
			meta := metadata[sid]
			if meta == nil {
				meta = M{"timestamp": d["created_at_ms"]}
			}
			if err = i.prepareSession(tx, sid, meta, entry.source, false); err != nil {
				return err
			}
			touched[sid] = true
		}
		if str(metadata[sid]["history_mode"]) == "legacy" {
			if _, e := one(tx, "SELECT path FROM files WHERE session_id=? AND mode IN ('response','hybrid','items','events') LIMIT 1", sid); e == nil {
				continue
			}
		}
		marker := hash([]byte(jsonText(d)))
		if !full {
			if previous, e := one(tx, "SELECT marker FROM db_items WHERE source=? AND thread_id=? AND item_id=?", entry.path, sid, d["item_id"]); e == nil && str(previous["marker"]) == marker {
				continue
			}
		}
		body, e := one(src, "SELECT item_json FROM thread_items WHERE thread_id=? AND item_id=?", sid, d["item_id"])
		if e != nil {
			return e
		}
		d["item_json"] = body["item_json"]
		var item M
		if err = json.Unmarshal([]byte(str(d["item_json"])), &item); err != nil {
			return err
		}
		changed, e := i.item(tx, sid, item, integer(d["rollout_ordinal"]), stamp(d["created_at_ms"]), "item:"+str(first(item["id"], d["item_id"])))
		if e != nil {
			return e
		}
		if changed {
			raw := []byte(str(d["item_json"]) + "\n")
			logical := entry.path + "::" + sid + "::" + str(d["item_id"])
			version := integer(first(d["updated_at_ordinal"], d["rollout_ordinal"]))
			if err = i.Store.addRaw(tx, logical, sid, version, 0, int64(len(raw)), raw); err != nil {
				return err
			}
			added++
		}
		if _, err = tx.Exec("INSERT INTO db_items(source,thread_id,item_id,marker) VALUES (?,?,?,?) ON CONFLICT(source,thread_id,item_id) DO UPDATE SET marker=excluded.marker", entry.path, sid, d["item_id"], marker); err != nil {
			return err
		}
	}
	for sid := range touched {
		if err = i.Store.recount(tx, sid); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO files(path,session_id,mode,last_sync) VALUES (?,?,'database',?) ON CONFLICT(path) DO UPDATE SET last_sync=excluded.last_sync,missing=0", entry.path+"::"+sid, sid, now()); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, signature); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	tx = nil
	i.update(func(s *SyncStatus) { s.Imported += added })
	return nil
}
func (i *Importer) chatgpt(path string, full bool) error {
	i.Store.importMu.Lock()
	defer i.Store.importMu.Unlock()
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !full {
		if previous, e := one(i.Store.DB, "SELECT size,mtime FROM files WHERE path=?", path); e == nil && integer(previous["size"]) == stat.Size() && integer(previous["mtime"]) == stat.ModTime().UnixNano() {
			i.update(func(s *SyncStatus) { s.Skipped++ })
			return nil
		}
	}
	var reader io.Reader
	var cleanup func()
	if strings.ToLower(filepath.Ext(path)) == ".zip" {
		archive, e := zip.OpenReader(path)
		if e != nil {
			return e
		}
		defer archive.Close()
		for _, f := range archive.File {
			if filepath.Base(f.Name) == "conversations.json" {
				if f.UncompressedSize64 > 1024*1024*1024 {
					return errors.New("导出 JSON 大于 1 GB，请添加解压后的目录")
				}
				r, e := f.Open()
				if e != nil {
					return e
				}
				reader = r
				cleanup = func() { r.Close() }
				break
			}
		}
		if reader == nil {
			return errors.New("ZIP 中未找到 conversations.json")
		}
	} else {
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		reader = f
		cleanup = func() { f.Close() }
	}
	defer cleanup()
	br := bufio.NewReader(reader)
	prefix, _ := br.Peek(3)
	if bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		br.Discard(3)
	}
	decoder := json.NewDecoder(br)
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('[') {
		return errors.New("不是 ChatGPT conversations.json 格式；应为包含 mapping 的对话数组")
	}
	added := 0
	for decoder.More() {
		var c M
		if err = decoder.Decode(&c); err != nil {
			return err
		}
		if _, ok := c["mapping"]; !ok {
			return errors.New("导出记录缺少 mapping")
		}
		n, e := i.chatgptConversation(c, path, stat.ModTime().UnixNano(), full, "云端导出", nil)
		if e != nil {
			return e
		}
		added += n
	}
	if _, err = decoder.Token(); err != nil {
		return err
	}
	_, err = i.Store.Write.Exec("INSERT INTO files(path,size,mtime,mode,last_sync) VALUES (?,?,?,'chatgpt',?) ON CONFLICT(path) DO UPDATE SET size=excluded.size,mtime=excluded.mtime,last_sync=excluded.last_sync,missing=0", path, stat.Size(), stat.ModTime().UnixNano(), now())
	i.update(func(s *SyncStatus) { s.Imported += added })
	return err
}

func (i *Importer) chatgptConversation(c M, path string, generation int64, full bool, platform string, original []byte) (int, error) {
	if _, ok := c["mapping"]; !ok {
		messages, ok := c["messages"].([]any)
		if !ok {
			return 0, errors.New("ChatGPT 缓存缺少 mapping 或 messages")
		}
		nodes := M{}
		previous := ""
		for _, value := range messages {
			m := obj(value)
			id := str(m["id"])
			if id == "" {
				return 0, errors.New("ChatGPT 缓存消息缺少 ID")
			}
			parent := first(obj(m["metadata"])["parent_id"], previous)
			nodes[id] = M{"message": m, "parent": parent}
			previous = id
		}
		c["mapping"] = nodes
		if nodes[str(c["current_node"])] == nil {
			c["current_node"] = previous
		}
	}
	added := 0
	sid := "chatgpt:" + str(first(c["id"], c["conversation_id"], hash([]byte(jsonText(c)))))
	nodes := obj(c["mapping"])
	active := map[string]bool{}
	cursor := str(c["current_node"])
	for cursor != "" && nodes[cursor] != nil && !active[cursor] {
		active[cursor] = true
		cursor = str(obj(nodes[cursor])["parent"])
	}
	tx, e := i.Store.Write.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	allowed, e := importSession(tx, sid, full)
	if e != nil {
		tx.Rollback()
		return 0, e
	}
	if !allowed {
		tx.Rollback()
		i.update(func(s *SyncStatus) { s.Skipped++ })
		return 0, nil
	}
	staleCache := false
	if original != nil && stamp(c["update_time"]) > 0 {
		if previous, err := one(tx, "SELECT updated,title,model FROM sessions WHERE id=?", sid); err == nil && number(previous["updated"]) > stamp(c["update_time"]) {
			staleCache = true
			c["title"], c["default_model_slug"] = previous["title"], previous["model"]
		}
	}
	e = i.Store.session(tx, sid, M{"title": str(first(c["title"], "ChatGPT 对话")), "project": "ChatGPT", "platform": platform, "provider": "ChatGPT", "created": stamp(c["create_time"]), "updated": stamp(c["update_time"]), "model": str(c["default_model_slug"]), "metadata": jsonText(M{"current_node": c["current_node"], "branch_count": len(nodes), "desktop_cache": original != nil, "cache_partial": obj(c["page_info"])["has_previous_page"] == true || obj(c["page_info"])["has_next_page"] == true})})
	if e != nil {
		tx.Rollback()
		return 0, e
	}
	ids := []string{}
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.SliceStable(ids, func(a, b int) bool {
		ta := stamp(obj(obj(nodes[ids[a]])["message"])["create_time"])
		tb := stamp(obj(obj(nodes[ids[b]])["message"])["create_time"])
		if ta == tb {
			return ids[a] < ids[b]
		}
		return ta < tb
	})
	for pos, nid := range ids {
		if staleCache {
			if _, err := one(tx, "SELECT id FROM messages WHERE session_id=? AND identity=?", sid, "node:"+nid); err == nil {
				continue
			} else if !errors.Is(err, errNotFound) {
				return 0, err
			}
		}
		node := obj(nodes[nid])
		m := obj(node["message"])
		if len(m) == 0 {
			continue
		}
		role := str(obj(m["author"])["role"])
		if role != "user" && role != "assistant" && role != "tool" {
			role = "context"
		}
		content := obj(m["content"])
		if role == "assistant" && (str(content["content_type"]) == "thoughts" || str(content["content_type"]) == "reasoning_recap") {
			role = "reasoning"
		}
		if obj(m["metadata"])["is_visually_hidden_from_conversation"] == true {
			role = "context"
		}
		body := textContent(content["parts"])
		if body == "" {
			body = str(content["text"])
		}
		if body == "" {
			body = textContent(content["content"])
		}
		if body == "" {
			for _, thought := range array(content["thoughts"]) {
				if t := str(first(obj(thought)["content"], obj(thought)["summary"], textContent(thought))); t != "" {
					if body != "" {
						body += "\n"
					}
					body += t
				}
			}
		}
		assets, e := i.assets(tx, content["parts"], nil)
		if e != nil {
			tx.Rollback()
			return 0, e
		}
		meta := M{"branch": len(active) > 0 && !active[nid], "parent": node["parent"], "assets": assets, "node_id": nid}
		changed, e := i.Store.message(tx, sid, "node:"+nid, int64(pos), role, body, stamp(m["create_time"]), str(first(content["content_type"], "message")), str(obj(m["metadata"])["phase"]), meta)
		if e != nil {
			tx.Rollback()
			return 0, e
		}
		if changed {
			added++
		}
	}
	raw := original
	if raw == nil {
		raw = []byte(jsonText(c) + "\n")
	}
	if e = i.Store.addRaw(tx, path+"::"+sid, sid, generation, 0, int64(len(raw)), raw); e != nil {
		tx.Rollback()
		return 0, e
	}
	if _, e = tx.Exec("INSERT INTO files(path,session_id,mode,last_sync) VALUES (?,?,'chatgpt_node',?) ON CONFLICT(path) DO UPDATE SET last_sync=excluded.last_sync,missing=0", path+"::"+sid, sid, now()); e != nil {
		tx.Rollback()
		return 0, e
	}
	if _, e = tx.Exec("WITH ordered AS (SELECT id,row_number() OVER (ORDER BY timestamp,id)-1 AS pos FROM messages WHERE session_id=?) UPDATE messages SET position=(SELECT pos FROM ordered WHERE ordered.id=messages.id) WHERE session_id=?", sid, sid); e != nil {
		tx.Rollback()
		return 0, e
	}
	if e = i.Store.recount(tx, sid); e != nil {
		tx.Rollback()
		return 0, e
	}
	if e = tx.Commit(); e != nil {
		return 0, e
	}
	return added, nil
}
