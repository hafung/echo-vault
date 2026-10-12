package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type M = map[string]any
type DBQuery interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}
type Store struct {
	DB            *sql.DB
	Write         *sql.DB
	Path          string
	DialogueIndex bool
}

var errNotFound = errors.New("记录不存在")
var whitespace = regexp.MustCompile(`\s+`)
var binaryText = regexp.MustCompile(`[A-Za-z0-9+/=_-]{256,}`)
var queryPattern = regexp.MustCompile(`"([^"]+)"|(\S+)`)

func openStore(path string) (*Store, error) {
	path, _ = filepath.Abs(path)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String() + "?_pragma=busy_timeout(20000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-16384)&_pragma=mmap_size(268435456)"
	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	if _, err = write.Exec(schema); err != nil {
		write.Close()
		return nil, err
	}
	read, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(6)
	read.SetMaxIdleConns(6)
	os.Chmod(path, 0600)
	var n int
	write.QueryRow("SELECT count(*) FROM messages").Scan(&n)
	if n == 0 {
		if _, err = write.Exec("INSERT OR IGNORE INTO settings(key,value) VALUES ('dialogue_index','1')"); err != nil {
			return nil, err
		}
	}
	var ready string
	write.QueryRow("SELECT value FROM settings WHERE key='dialogue_index'").Scan(&ready)
	return &Store{DB: read, Write: write, Path: path, DialogueIndex: ready == "1"}, nil
}
func (s *Store) Close() { s.DB.Close(); s.Write.Close() }
func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}
func number(v any) float64 {
	switch t := v.(type) {
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float64:
		return t
	case json.Number:
		n, _ := t.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(t, 64)
		return n
	}
	return 0
}
func integer(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		if i, e := n.Int64(); e == nil {
			return i
		}
	case string:
		if i, e := strconv.ParseInt(n, 10, 64); e == nil {
			return i
		}
	}
	return int64(number(v))
}
func obj(v any) M {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return M{}
}
func array(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}
func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
func now() float64 { return float64(time.Now().UnixMilli()) / 1000 }
func stamp(v any) float64 {
	if n := number(v); n != 0 {
		if n > 1e12 {
			return n / 1000
		}
		return n
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02"} {
		if t, err := time.Parse(layout, str(v)); err == nil {
			return float64(t.UnixMilli()) / 1000
		}
	}
	return 0
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func pack(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, 3)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}
func unpack(b []byte) (string, error) {
	r, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer r.Close()
	v, err := io.ReadAll(r)
	return string(v), err
}
func readable(s string) string {
	return binaryText.ReplaceAllString(s, " [二进制内容保留在原始记录中] ")
}
func shortTokens(text string) string {
	r := []rune(strings.ToLower(text))
	tokens := make(map[string]struct{}, len(r)/2)
	for i, c := range r {
		tokens["u"+strconv.FormatInt(int64(c), 16)] = struct{}{}
		if i > 0 {
			tokens["b"+strconv.FormatInt(int64(r[i-1]), 16)+"x"+strconv.FormatInt(int64(c), 16)] = struct{}{}
		}
	}
	var b strings.Builder
	for token := range tokens {
		b.WriteString(token)
		b.WriteByte(' ')
	}
	return b.String()
}
func queryParts(q string) []string {
	var terms []string
	for _, m := range queryPattern.FindAllStringSubmatch(strings.TrimSpace(q), 16) {
		t := m[1]
		if t == "" {
			t = m[2]
		}
		terms = append(terms, t)
	}
	return terms
}
func searchClause(q, column string, dialogue bool) (string, []any) {
	longTable, shortTable := "search_long", "search_short"
	if dialogue {
		longTable, shortTable = "dialogue_long", "dialogue_short"
	}
	var longs, shorts []string
	for _, term := range queryParts(q) {
		r := []rune(strings.ToLower(term))
		if len(r) >= 3 {
			longs = append(longs, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		} else if len(r) == 1 {
			shorts = append(shorts, "u"+strconv.FormatInt(int64(r[0]), 16))
		} else if len(r) == 2 {
			shorts = append(shorts, "b"+strconv.FormatInt(int64(r[0]), 16)+"x"+strconv.FormatInt(int64(r[1]), 16))
		}
	}
	var clauses []string
	var args []any
	if len(longs) > 0 {
		clauses = append(clauses, column+" IN (SELECT rowid FROM "+longTable+" WHERE "+longTable+" MATCH ?)")
		args = append(args, strings.Join(longs, " AND "))
	}
	if len(shorts) > 0 {
		clauses = append(clauses, column+" IN (SELECT rowid FROM "+shortTable+" WHERE "+shortTable+" MATCH ?)")
		args = append(args, strings.Join(shorts, " AND "))
	}
	if len(clauses) == 0 {
		return "1", args
	}
	return strings.Join(clauses, " AND "), args
}
func rowsMap(rows *sql.Rows) ([]M, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := []M{}
	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		m := M{}
		for i, c := range cols {
			m[c] = values[i]
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
func query(db DBQuery, q string, args ...any) ([]M, error) {
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	return rowsMap(rows)
}
func one(db DBQuery, q string, args ...any) (M, error) {
	rows, err := query(db, q, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errNotFound
	}
	return rows[0], nil
}

func (s *Store) session(db DBQuery, sid string, v M) error {
	if str(v["title"]) == "" {
		v["title"] = "未命名对话"
	}
	if number(v["created"]) == 0 {
		v["created"] = now()
	}
	if number(v["updated"]) == 0 {
		v["updated"] = v["created"]
	}
	allowed := map[string]bool{"title": true, "project": true, "cwd": true, "platform": true, "provider": true, "model": true, "created": true, "updated": true, "archived": true, "parent_id": true, "metadata": true}
	keys := []string{}
	for k := range v {
		if allowed[k] && v[k] != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	args := []any{sid}
	fields := []string{"id"}
	holders := []string{"?"}
	updates := []string{}
	for _, k := range keys {
		args = append(args, v[k])
		fields = append(fields, k)
		holders = append(holders, "?")
		if k != "created" && k != "updated" {
			updates = append(updates, k+"=excluded."+k)
		}
	}
	updates = append(updates, "created=min(sessions.created,excluded.created)", "updated=max(sessions.updated,excluded.updated)")
	_, err := db.Exec("INSERT INTO sessions("+strings.Join(fields, ",")+") VALUES ("+strings.Join(holders, ",")+") ON CONFLICT(id) DO UPDATE SET "+strings.Join(updates, ","), args...)
	return err
}
func (s *Store) message(db DBQuery, sid, identity string, pos int64, role, body string, ts float64, kind, phase string, metadata M) (bool, error) {
	if strings.TrimSpace(body) == "" && len(array(metadata["assets"])) == 0 {
		return false, nil
	}
	digest := hash([]byte(body))
	previous, err := one(db, "SELECT id,body_hash,metadata FROM messages WHERE session_id=? AND identity=?", sid, identity)
	if err != nil && !errors.Is(err, errNotFound) {
		return false, err
	}
	metaJSON := jsonText(metadata)
	if previous != nil && str(previous["body_hash"]) == digest {
		if str(previous["metadata"]) != metaJSON {
			_, err = db.Exec("UPDATE messages SET metadata=? WHERE id=?", metaJSON, previous["id"])
		}
		return false, err
	}
	preview := clip(whitespace.ReplaceAllString(readable(body), " "), 420)
	var id int64
	if previous != nil {
		id = integer(previous["id"])
		_, err = db.Exec("UPDATE messages SET body=?,body_hash=?,preview=?,metadata=?,phase=? WHERE id=?", pack([]byte(body)), digest, preview, metaJSON, phase, id)
		if err != nil {
			return false, err
		}
		for _, table := range []string{"search_long", "search_short", "dialogue_long", "dialogue_short"} {
			if _, err = db.Exec("DELETE FROM "+table+" WHERE rowid=?", id); err != nil {
				return false, err
			}
		}
	} else {
		var result sql.Result
		result, err = db.Exec("INSERT INTO messages(session_id,identity,position,role,kind,phase,timestamp,body,body_hash,preview,metadata) VALUES (?,?,?,?,?,?,?,?,?,?,?)", sid, identity, pos, role, kind, phase, ts, pack([]byte(body)), digest, preview, metaJSON)
		if err != nil {
			return false, err
		}
		id, _ = result.LastInsertId()
	}
	text := readable(body)
	if _, err = db.Exec("INSERT INTO search_long(rowid,text) VALUES (?,?)", id, text); err != nil {
		return false, err
	}
	_, err = db.Exec("INSERT INTO search_short(rowid,text) VALUES (?,?)", id, shortTokens(text))
	if err != nil {
		return false, err
	}
	if role == "user" || role == "assistant" {
		if _, err = db.Exec("INSERT INTO dialogue_long(rowid,text) VALUES (?,?)", id, text); err != nil {
			return false, err
		}
		_, err = db.Exec("INSERT INTO dialogue_short(rowid,text) VALUES (?,?)", id, shortTokens(text))
	}
	return true, err
}
func (s *Store) recount(db DBQuery, sid string) error {
	_, err := db.Exec(`UPDATE sessions SET total_count=(SELECT count(*) FROM messages WHERE session_id=?),message_count=(SELECT count(*) FROM messages WHERE session_id=? AND role IN ('user','assistant')),updated=max(updated,coalesce((SELECT max(timestamp) FROM messages WHERE session_id=?),0)),preview=coalesce((SELECT preview FROM messages WHERE session_id=? AND role='user' ORDER BY position,id LIMIT 1),'') WHERE id=?`, sid, sid, sid, sid, sid)
	return err
}

const visibleSessions = "coalesce((SELECT hidden FROM session_visibility WHERE session_id=s.id),s.archived)=0 AND NOT EXISTS (SELECT 1 FROM hidden_projects WHERE project=s.project)"

func (s *Store) stats(ctx context.Context, includeHidden ...bool) (M, error) {
	where := "1"
	if len(includeHidden) == 0 || !includeHidden[0] {
		where = visibleSessions
	}
	v, err := one(s.DB, "SELECT count(*) sessions,coalesce(sum(message_count),0) messages,coalesce(sum(total_count),0) events,count(distinct project) projects,coalesce(sum(favorite),0) favorites FROM sessions s WHERE "+where)
	if err != nil {
		return nil, err
	}
	for key, q := range map[string]string{"projects_list": "SELECT project,count(*) count,max(updated) updated,EXISTS (SELECT 1 FROM hidden_projects WHERE project=s.project) hidden FROM sessions s WHERE " + where + " GROUP BY project ORDER BY updated DESC,project", "platforms": "SELECT platform,count(*) count FROM sessions s WHERE " + where + " GROUP BY platform", "providers": "SELECT provider,count(*) count FROM sessions s WHERE " + where + " GROUP BY provider"} {
		v[key], err = query(s.DB, q)
		if err != nil {
			return nil, err
		}
	}
	var last sql.NullFloat64
	if err = s.DB.QueryRow("SELECT max(finished) FROM sync_runs").Scan(&last); err != nil {
		return nil, err
	}
	if last.Valid {
		v["last_sync"] = last.Float64
	} else {
		v["last_sync"] = nil
	}
	var count int64
	for _, p := range []string{s.Path, s.Path + "-wal"} {
		if info, e := os.Stat(p); e == nil {
			count += info.Size()
		}
	}
	v["db_bytes"] = count
	err = s.DB.QueryRow("SELECT coalesce(sum(end-start),0) FROM raw_chunks").Scan(&count)
	v["raw_bytes"] = count
	return v, err
}
func paramInt(p url.Values, key string, def, min, max int) int {
	v, err := strconv.Atoi(p.Get(key))
	if err != nil {
		v = def
	}
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return v
}
func (s *Store) sessions(ctx context.Context, p url.Values) (M, error) {
	started := time.Now()
	where := []string{"1"}
	args := []any{}
	if p.Get("hidden") != "1" {
		where = append(where, visibleSessions)
	}
	for _, k := range []string{"project", "platform", "provider"} {
		if p.Get(k) != "" {
			where = append(where, "s."+k+"=?")
			args = append(args, p.Get(k))
		}
	}
	if p.Get("favorite") == "1" {
		where = append(where, "s.favorite=1")
	}
	if p.Get("from") != "" {
		where = append(where, "s.updated>=?")
		args = append(args, stamp(p.Get("from")))
	}
	if p.Get("to") != "" {
		where = append(where, "s.created<=?")
		args = append(args, stamp(p.Get("to"))+86400)
	}
	q := clip(strings.TrimSpace(p.Get("q")), 500)
	cte := ""
	join := ""
	visibility := "coalesce((SELECT hidden FROM session_visibility WHERE session_id=s.id),s.archived) hidden,EXISTS (SELECT 1 FROM hidden_projects WHERE project=s.project) project_hidden,"
	selects := "s.*," + visibility + "NULL match_id,0 matches"
	if q != "" {
		clause, qa := searchClause(q, "m.id", s.DialogueIndex && p.Get("all") != "1")
		roles := ""
		if p.Get("all") != "1" {
			roles = "AND m.role IN ('user','assistant')"
		}
		cte = "WITH hits AS (SELECT session_id,min(id) match_id,count(*) matches FROM messages m WHERE " + clause + " " + roles + " GROUP BY session_id) "
		args = append(qa, args...)
		join = " LEFT JOIN hits h ON h.session_id=s.id"
		selects = "s.*," + visibility + "h.match_id,coalesce(h.matches,0) matches"
		titles := []string{}
		for _, term := range queryParts(q) {
			titles = append(titles, "instr(lower(s.title||' '||s.project),lower(?))>0")
			args = append(args, term)
		}
		where = append(where, "(h.session_id IS NOT NULL OR ("+strings.Join(titles, " AND ")+"))")
	}
	offset := paramInt(p, "offset", 0, 0, 10000000)
	limit := paramInt(p, "limit", 35, 1, 60)
	direction := "DESC"
	if p.Get("sort") == "oldest" {
		direction = "ASC"
	}
	args = append(args, limit+1, offset)
	rows, err := s.DB.QueryContext(ctx, cte+"SELECT "+selects+" FROM sessions s"+join+" WHERE "+strings.Join(where, " AND ")+" ORDER BY s.updated "+direction+",s.id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rowsMap(rows)
	if err != nil {
		return nil, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	for _, m := range items {
		delete(m, "metadata")
		m["match_preview"] = ""
		if q != "" && m["match_id"] != nil {
			preview, e := one(s.DB, "SELECT preview FROM messages WHERE id=?", m["match_id"])
			if e != nil {
				return nil, e
			}
			body := str(preview["preview"])
			needBody := false
			for _, term := range queryParts(q) {
				if !strings.Contains(strings.ToLower(body), strings.ToLower(term)) {
					needBody = true
					break
				}
			}
			if needBody {
				body, e = s.body(integer(m["match_id"]))
			}
			if e != nil {
				return nil, e
			}
			r := []rune(body)
			low := strings.ToLower(body)
			pos := len(r)
			for _, term := range queryParts(q) {
				i := strings.Index(low, strings.ToLower(term))
				if i >= 0 {
					n := len([]rune(low[:i]))
					if n < pos {
						pos = n
					}
				}
			}
			if pos == len(r) {
				pos = 0
			}
			begin := pos - 70
			if begin < 0 {
				begin = 0
			}
			end := pos + 250
			if end > len(r) {
				end = len(r)
			}
			text := string(r[begin:end])
			if begin > 0 {
				text = "…" + text
			}
			m["match_preview"] = text
		}
	}
	return M{"items": items, "more": more, "offset": offset + len(items), "elapsed_ms": float64(time.Since(started).Microseconds()) / 1000}, nil
}
func (s *Store) messages(ctx context.Context, sid string, p url.Values) (M, error) {
	visibility := ""
	if p.Get("hidden") != "1" {
		visibility = " AND " + visibleSessions
	}
	session, err := one(s.DB, "SELECT s.*,coalesce((SELECT hidden FROM session_visibility WHERE session_id=s.id),s.archived) hidden,EXISTS (SELECT 1 FROM hidden_projects WHERE project=s.project) project_hidden FROM sessions s WHERE s.id=?"+visibility, sid)
	if err != nil {
		return nil, err
	}
	where := "session_id=?"
	args := []any{sid}
	if p.Get("all") != "1" {
		where += " AND role IN ('user','assistant')"
	}
	offset := paramInt(p, "offset", 0, 0, 10000000)
	limit := paramInt(p, "limit", 30, 1, 60)
	if p.Get("around") != "" {
		target, e := one(s.DB, "SELECT position,id FROM messages WHERE id=? AND session_id=?", p.Get("around"), sid)
		if e == nil {
			var n int
			err = s.DB.QueryRow("SELECT count(*) FROM messages WHERE "+where+" AND (position<? OR (position=? AND id<?))", append(args, target["position"], target["position"], target["id"])...).Scan(&n)
			if err != nil {
				return nil, err
			}
			offset = n - 5
			if offset < 0 {
				offset = 0
			}
		}
	}
	var total int
	if err = s.DB.QueryRow("SELECT count(*) FROM messages WHERE "+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT * FROM messages WHERE "+where+" ORDER BY position,id LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	items, err := rowsMap(rows)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		body, e := unpack(m["body"].([]byte))
		if e != nil {
			return nil, e
		}
		length := len([]rune(body))
		m["body"] = clip(body, 24000)
		m["truncated"] = length > 24000
		m["length"] = length
		var meta M
		if e = json.Unmarshal([]byte(str(m["metadata"])), &meta); e != nil {
			return nil, e
		}
		m["metadata"] = meta
	}
	var meta M
	json.Unmarshal([]byte(str(session["metadata"])), &meta)
	session["metadata"] = meta
	session["sources"], err = query(s.DB, "SELECT path,missing,last_sync FROM files WHERE session_id=?", sid)
	if err != nil {
		return nil, err
	}
	return M{"session": session, "items": items, "offset": offset, "total": total, "more": offset+len(items) < total}, nil
}
func (s *Store) body(id int64) (string, error) {
	var b []byte
	err := s.DB.QueryRow("SELECT body FROM messages WHERE id=?", id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return "", errNotFound
	}
	if err != nil {
		return "", err
	}
	return unpack(b)
}
func (s *Store) hits(ctx context.Context, sid string, p url.Values) ([]M, error) {
	if strings.TrimSpace(p.Get("q")) == "" {
		return []M{}, nil
	}
	clause, args := searchClause(clip(p.Get("q"), 500), "m.id", s.DialogueIndex && p.Get("all") != "1")
	roles := ""
	if p.Get("all") != "1" {
		roles = "AND role IN ('user','assistant')"
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT id,role,preview FROM messages m WHERE session_id=? AND "+clause+" "+roles+" ORDER BY position,id LIMIT 1000", append([]any{sid}, args...)...)
	if err != nil {
		return nil, err
	}
	return rowsMap(rows)
}
func (s *Store) export(sid, format string, w io.Writer) error {
	session, err := one(s.DB, "SELECT * FROM sessions WHERE id=?", sid)
	if err != nil {
		return err
	}
	if format == "raw" {
		rows, err := s.DB.Query("SELECT r.path,r.start,r.end,r.payload FROM raw_chunks r WHERE r.session_id=? AND r.generation=(SELECT max(x.generation) FROM raw_chunks x WHERE x.path=r.path) ORDER BY r.path,r.start,r.end DESC", sid)
		if err != nil {
			return err
		}
		defer rows.Close()
		var previousPath string
		var cursor int64
		for rows.Next() {
			var b []byte
			var sourcePath string
			var start, end int64
			if err = rows.Scan(&sourcePath, &start, &end, &b); err != nil {
				return err
			}
			if sourcePath != previousPath {
				previousPath = sourcePath
				cursor = 0
			}
			if end <= cursor {
				continue
			}
			text, e := s.rawBody(b)
			if e != nil {
				return e
			}
			if start < cursor {
				text = text[cursor-start:]
			}
			if _, e = io.WriteString(w, text); e != nil {
				return e
			}
			cursor = end
		}
		return rows.Err()
	}
	messages, err := query(s.DB, "SELECT * FROM messages WHERE session_id=? ORDER BY position,id", sid)
	if err != nil {
		return err
	}
	for _, m := range messages {
		body, e := unpack(m["body"].([]byte))
		if e != nil {
			return e
		}
		m["body"] = body
		var meta M
		json.Unmarshal([]byte(str(m["metadata"])), &meta)
		m["metadata"] = meta
	}
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(M{"session": session, "messages": messages})
	}
	fmt.Fprintf(w, "# %s\n\n%s\n\n", str(session["title"]), str(session["cwd"]))
	for _, m := range messages {
		role := str(m["role"])
		if role == "user" || role == "assistant" {
			name := "Codex / GPT"
			if role == "user" {
				name = "你"
			}
			if _, err = fmt.Fprintf(w, "## %s\n\n%s\n\n", name, str(m["body"])); err != nil {
				return err
			}
		}
	}
	return nil
}

const rawReference = "echo-blob:"

func (s *Store) indexDialogue() error {
	if _, err := s.Write.Exec("DROP TABLE IF EXISTS dialogue_long_next; DROP TABLE IF EXISTS dialogue_short_next; CREATE VIRTUAL TABLE dialogue_long_next USING fts5(text,content='',tokenize='trigram',contentless_delete=1); CREATE VIRTUAL TABLE dialogue_short_next USING fts5(text,content='',detail='none',contentless_delete=1)"); err != nil {
		return err
	}
	var last int64
	for {
		rows, err := query(s.DB, "SELECT id,body FROM messages WHERE id>? AND role IN ('user','assistant') ORDER BY id LIMIT 256", last)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		tx, err := s.Write.Begin()
		if err != nil {
			return err
		}
		for _, row := range rows {
			body, e := unpack(row["body"].([]byte))
			if e != nil {
				tx.Rollback()
				return e
			}
			text := readable(body)
			if _, e = tx.Exec("INSERT INTO dialogue_long_next(rowid,text) VALUES (?,?)", row["id"], text); e != nil {
				tx.Rollback()
				return e
			}
			if _, e = tx.Exec("INSERT INTO dialogue_short_next(rowid,text) VALUES (?,?)", row["id"], shortTokens(text)); e != nil {
				tx.Rollback()
				return e
			}
			last = integer(row["id"])
		}
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	tx, err := s.Write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DROP TABLE dialogue_long; DROP TABLE dialogue_short; ALTER TABLE dialogue_long_next RENAME TO dialogue_long; ALTER TABLE dialogue_short_next RENAME TO dialogue_short; INSERT INTO settings(key,value) VALUES ('dialogue_index','1') ON CONFLICT(key) DO UPDATE SET value='1'"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.DialogueIndex = true
	return nil
}

func (s *Store) addRaw(db DBQuery, path, sid string, generation, start, end int64, raw []byte) error {
	if _, e := one(db, "SELECT id FROM raw_chunks WHERE path=? AND generation=? AND start=? AND end=?", path, generation, start, end); e == nil {
		return nil
	} else if !errors.Is(e, errNotFound) {
		return e
	}
	digest := hash(raw)
	if _, e := one(db, "SELECT digest FROM raw_blobs WHERE digest=?", digest); errors.Is(e, errNotFound) {
		if _, e = db.Exec("INSERT OR IGNORE INTO raw_blobs(digest,payload) VALUES (?,?)", digest, pack(raw)); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	_, err := db.Exec("INSERT INTO raw_chunks(path,session_id,generation,start,end,payload) VALUES (?,?,?,?,?,?)", path, sid, generation, start, end, []byte(rawReference+digest))
	return err
}
func (s *Store) rawBody(payload []byte) (string, error) {
	if bytes.HasPrefix(payload, []byte(rawReference)) {
		row, err := one(s.DB, "SELECT payload FROM raw_blobs WHERE digest=?", string(payload[len(rawReference):]))
		if err != nil {
			return "", err
		}
		payload = row["payload"].([]byte)
	}
	return unpack(payload)
}
func (s *Store) deduplicate(progress func(int)) error {
	var last int64
	done := 0
	for {
		rows, err := query(s.DB, "SELECT id,payload FROM raw_chunks WHERE id>? ORDER BY id LIMIT 16", last)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		digests := make([]string, len(rows))
		failures := make([]error, len(rows))
		var workers sync.WaitGroup
		slots := make(chan struct{}, 4)
		for index, row := range rows {
			payload := row["payload"].([]byte)
			if bytes.HasPrefix(payload, []byte(rawReference)) {
				continue
			}
			workers.Add(1)
			go func(index int, payload []byte) {
				defer workers.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				raw, e := unpack(payload)
				if e != nil {
					failures[index] = e
					return
				}
				digests[index] = hash([]byte(raw))
			}(index, payload)
		}
		workers.Wait()
		for _, e := range failures {
			if e != nil {
				return e
			}
		}
		tx, err := s.Write.Begin()
		if err != nil {
			return err
		}
		for index, row := range rows {
			last = integer(row["id"])
			payload := row["payload"].([]byte)
			if bytes.HasPrefix(payload, []byte(rawReference)) {
				continue
			}
			digest := digests[index]
			if _, e := one(tx, "SELECT digest FROM raw_blobs WHERE digest=?", digest); errors.Is(e, errNotFound) {
				if _, e = tx.Exec("INSERT INTO raw_blobs(digest,payload) VALUES (?,?)", digest, payload); e != nil {
					tx.Rollback()
					return e
				}
			} else if e != nil {
				tx.Rollback()
				return e
			}
			if _, e := tx.Exec("UPDATE raw_chunks SET payload=? WHERE id=?", []byte(rawReference+digest), last); e != nil {
				tx.Rollback()
				return e
			}
			done++
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if progress != nil {
			progress(done)
		}
	}
	return nil
}

// Rebuild only derived search indexes, retaining all conversation bodies and raw source bytes.
// The original files are never read by this migration.
func (s *Store) reindex(progress func(int)) error {
	if _, err := s.Write.Exec("DROP TABLE IF EXISTS search_long_next; DROP TABLE IF EXISTS search_short_next; CREATE VIRTUAL TABLE search_long_next USING fts5(text,content='',tokenize='trigram',contentless_delete=1); CREATE VIRTUAL TABLE search_short_next USING fts5(text,content='',detail='none',contentless_delete=1);"); err != nil {
		return err
	}
	var last int64
	count := 0
	for {
		batch, err := query(s.DB, "SELECT id,body FROM messages WHERE id>? ORDER BY id LIMIT 256", last)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			break
		}
		tx, err := s.Write.Begin()
		if err != nil {
			return err
		}
		for _, m := range batch {
			body, e := unpack(m["body"].([]byte))
			if e != nil {
				tx.Rollback()
				return e
			}
			text := readable(body)
			if _, e = tx.Exec("INSERT INTO search_long_next(rowid,text) VALUES (?,?)", m["id"], text); e != nil {
				tx.Rollback()
				return e
			}
			if _, e = tx.Exec("INSERT INTO search_short_next(rowid,text) VALUES (?,?)", m["id"], shortTokens(text)); e != nil {
				tx.Rollback()
				return e
			}
			last = integer(m["id"])
			count++
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		if progress != nil {
			progress(count)
		}
	}
	tx, err := s.Write.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec("DROP TABLE search_long; DROP TABLE search_short; ALTER TABLE search_long_next RENAME TO search_long; ALTER TABLE search_short_next RENAME TO search_short; INSERT INTO settings(key,value) VALUES ('index_version','2') ON CONFLICT(key) DO UPDATE SET value='2'")
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return s.indexDialogue()
}
