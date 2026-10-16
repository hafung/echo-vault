package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const desktopCapturePort = 19222
const desktopCaptureBatch = 8
const desktopCheckpointPrefix = "desktop_capture_checkpoint:"

//go:embed scripts/capture-chatgpt-desktop.ps1
var desktopCaptureScript string

var desktopConversationID = regexp.MustCompile(`^[A-Za-z0-9-]{1,128}$`)

type desktopCheckpoint struct {
	Updated       float64 `json:"updated"`
	Attempt       float64 `json:"attempt"`
	RetryAfter    float64 `json:"retry_after"`
	Complete      bool    `json:"complete"`
	Cursor        string  `json:"cursor,omitempty"`
	Streaming     bool    `json:"streaming,omitempty"`
	MissingBodies int     `json:"missing_bodies,omitempty"`
}

type sessionSyncResult struct {
	Complete      bool `json:"complete"`
	Pages         int  `json:"pages"`
	Messages      int  `json:"messages"`
	Streaming     bool `json:"streaming"`
	MissingBodies int  `json:"missing_bodies"`
}

func desktopCaptureDue(checkpoint desktopCheckpoint, updated, timestamp float64) bool {
	if checkpoint.RetryAfter > timestamp && checkpoint.Updated >= updated {
		return false
	}
	interval := float64(24 * 60 * 60)
	// A fresh answer may keep its conversation timestamp while tokens arrive.
	if checkpoint.Streaming || checkpoint.MissingBodies > 0 || timestamp-updated < 60*60 {
		interval = automaticSyncInterval
	}
	return !checkpoint.Complete || checkpoint.Updated < updated || timestamp-checkpoint.Attempt >= interval
}

type desktopCaptureJob struct {
	ID         string `json:"id"`
	Cursor     string `json:"cursor,omitempty"`
	title      string
	updated    float64
	checkpoint desktopCheckpoint
	priority   int
}

type desktopRecentConversation struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	UpdateTime any    `json:"update_time"`
}

type desktopCaptureResult struct {
	Kind         string                      `json:"kind"`
	ID           string                      `json:"id"`
	Complete     bool                        `json:"complete"`
	Cursor       string                      `json:"cursor"`
	Pages        int                         `json:"pages"`
	Status       int                         `json:"status"`
	Error        string                      `json:"error"`
	Conversation M                           `json:"conversation"`
	Recent       []desktopRecentConversation `json:"recent"`
}

func desktopCaptureSupported() bool { return runtime.GOOS == "windows" || isWSL() }

func (i *Importer) desktopCaptureSettings() (M, error) {
	row, err := one(i.Store.DB, "SELECT value FROM settings WHERE key='desktop_capture'")
	if err != nil && !errors.Is(err, errNotFound) {
		return nil, err
	}
	return M{"enabled": str(row["value"]) == "1", "supported": desktopCaptureSupported(), "port": desktopCapturePort}, nil
}

func (i *Importer) setDesktopCapture(enabled bool) error {
	if enabled && !desktopCaptureSupported() {
		return errors.New("桌面同步与历史补全目前支持 Windows 和 WSL")
	}
	value := "0"
	if enabled {
		value = "1"
	}
	_, err := i.Store.Write.Exec("INSERT INTO settings(key,value) VALUES ('desktop_capture',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value)
	return err
}

func (i *Importer) desktopCaptureCandidates(full bool, timestamp float64, recent ...desktopRecentConversation) ([]desktopCaptureJob, error) {
	rows, err := query(i.Store.DB, `SELECT s.id,s.title,s.updated,c.value AS checkpoint FROM sessions s
		LEFT JOIN settings c ON c.key=? || s.id
		WHERE s.provider='ChatGPT' AND s.platform='Windows' AND json_extract(s.metadata,'$.desktop_cache')=1`, desktopCheckpointPrefix)
	if err != nil {
		return nil, err
	}
	byID := map[string]desktopCaptureJob{}
	for _, row := range rows {
		id := strings.TrimPrefix(str(row["id"]), "chatgpt:")
		if !desktopConversationID.MatchString(id) {
			continue
		}
		var checkpoint desktopCheckpoint
		json.Unmarshal([]byte(str(row["checkpoint"])), &checkpoint)
		updated := number(row["updated"])
		if !full && !desktopCaptureDue(checkpoint, updated, timestamp) {
			continue
		}
		cursor := checkpoint.Cursor
		if full || checkpoint.Updated != updated || checkpoint.Complete {
			cursor = ""
		}
		priority := 0
		if checkpoint.Streaming || checkpoint.MissingBodies > 0 || timestamp-updated < 60*60 {
			priority = 1
			if checkpoint.Updated < updated {
				priority = 2
			}
		}
		byID[id] = desktopCaptureJob{ID: id, Cursor: cursor, title: str(row["title"]), updated: updated, checkpoint: checkpoint, priority: priority}
	}
	for _, conversation := range recent {
		id := conversation.ID
		if !desktopConversationID.MatchString(id) {
			continue
		}
		// Discovery never creates title-only sessions or restores local deletions.
		if !full {
			if _, e := one(i.Store.DB, "SELECT session_id FROM deleted_sessions WHERE session_id=?", "chatgpt:"+id); e == nil {
				continue
			} else if !errors.Is(e, errNotFound) {
				return nil, e
			}
		}
		row, e := one(i.Store.DB, "SELECT value FROM settings WHERE key=?", desktopCheckpointPrefix+"chatgpt:"+id)
		if e != nil && !errors.Is(e, errNotFound) {
			return nil, e
		}
		var checkpoint desktopCheckpoint
		json.Unmarshal([]byte(str(row["value"])), &checkpoint)
		updated := stamp(conversation.UpdateTime)
		if local, e := one(i.Store.DB, "SELECT updated FROM sessions WHERE id=?", "chatgpt:"+id); e == nil {
			updated = max(updated, number(local["updated"]))
		} else if !errors.Is(e, errNotFound) {
			return nil, e
		}
		// Manual sync rechecks fresh answers even when their update_time has not
		// changed. Automatic runs retain their normal interval and failure backoff.
		fresh := timestamp-updated < 60*60 || checkpoint.Streaming || checkpoint.MissingBodies > 0
		manual := !i.status().Automatic && fresh && checkpoint.RetryAfter <= timestamp
		if !full && !manual && !desktopCaptureDue(checkpoint, updated, timestamp) {
			continue
		}
		cursor := checkpoint.Cursor
		if full || checkpoint.Updated != updated || checkpoint.Complete {
			cursor = ""
		}
		priority := 0
		if fresh {
			priority = 1
		}
		if checkpoint.Attempt == 0 || checkpoint.Updated < updated {
			priority = 2
		}
		byID[id] = desktopCaptureJob{ID: id, Cursor: cursor, title: conversation.Title, updated: updated, checkpoint: checkpoint, priority: priority}
	}
	jobs := make([]desktopCaptureJob, 0, len(byID))
	for _, job := range byID {
		jobs = append(jobs, job)
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		if jobs[a].priority != jobs[b].priority {
			return jobs[a].priority > jobs[b].priority
		}
		if jobs[a].priority > 0 && jobs[a].updated != jobs[b].updated {
			return jobs[a].updated > jobs[b].updated
		}
		if jobs[a].checkpoint.Attempt == jobs[b].checkpoint.Attempt && jobs[a].updated != jobs[b].updated {
			return jobs[a].updated > jobs[b].updated
		}
		if jobs[a].checkpoint.Attempt != jobs[b].checkpoint.Attempt {
			return jobs[a].checkpoint.Attempt < jobs[b].checkpoint.Attempt
		}
		return jobs[a].ID < jobs[b].ID
	})
	return jobs, nil
}

// Explicit reads bypass the automatic batch, completion cache and retry delay.
// Partial pagination can continue; a previously completed read starts afresh.
func (i *Importer) startSessionSync(sid string) (bool, error) {
	row, err := one(i.Store.DB, "SELECT id,title,updated,provider,platform,metadata FROM sessions WHERE id=?", sid)
	if err != nil {
		return false, err
	}
	var metadata M
	json.Unmarshal([]byte(str(row["metadata"])), &metadata)
	id := strings.TrimPrefix(sid, "chatgpt:")
	if !strings.HasPrefix(sid, "chatgpt:") || !desktopConversationID.MatchString(id) || row["provider"] != "ChatGPT" || row["platform"] != "Windows" || metadata["desktop_cache"] != true {
		return false, errors.New("单会话重新同步目前支持 Windows Desktop Chat 对话")
	}
	settings, err := i.desktopCaptureSettings()
	if err != nil {
		return false, err
	}
	if settings["enabled"] != true {
		return false, errors.New("请先在数据与来源启用桌面同步与历史补全，并通过 Start ChatGPT Enhanced.cmd 启动 Desktop")
	}
	job := desktopCaptureJob{ID: id, title: str(row["title"]), updated: number(row["updated"])}
	checkpoint, err := one(i.Store.DB, "SELECT value FROM settings WHERE key=?", desktopCheckpointPrefix+sid)
	if err != nil && !errors.Is(err, errNotFound) {
		return false, err
	}
	json.Unmarshal([]byte(str(checkpoint["value"])), &job.checkpoint)
	if !job.checkpoint.Complete && job.checkpoint.Updated == job.updated {
		job.Cursor = job.checkpoint.Cursor
	}
	if !i.claimSync(false, now()) {
		return false, nil
	}
	i.update(func(s *SyncStatus) {
		s.SessionID = sid
		s.Stage = "chatgpt_desktop_live"
		s.File = job.title
		s.Total = 1
		s.DesktopPending = 1
	})
	go func() {
		defer i.finishSync()
		i.captureDesktopJobs([]desktopCaptureJob{job}, false, true)
	}()
	return true, nil
}

func desktopUnavailable(reason string) error {
	switch reason {
	case "module":
		return errors.New("当前 Desktop 版本的读取模块不兼容；请更新 GPT Vault 后重试")
	case "connection":
		return errors.New("无法连接 Desktop 的已登录页面；请关闭多余窗口后重试，或重新运行 Start ChatGPT Enhanced.cmd")
	case "page":
		return errors.New("Desktop 尚无可读取页面；请打开已登录的 Chat 页面后重试")
	default:
		return errors.New("桌面增强入口未就绪；请通过 Start ChatGPT Enhanced.cmd 启动已登录的 Desktop")
	}
}

func (i *Importer) saveDesktopCheckpoint(job desktopCaptureJob, checkpoint desktopCheckpoint) error {
	_, err := i.Store.Write.Exec("INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", desktopCheckpointPrefix+"chatgpt:"+job.ID, jsonText(checkpoint))
	return err
}

func readDesktopCapture(ctx context.Context, jobs []desktopCaptureJob, accept func(desktopCaptureResult) error) error {
	input, err := json.Marshal(M{"port": desktopCapturePort, "jobs": jobs, "discover": jobs == nil})
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encodedPowerShell(desktopCaptureScript))
	command.Stdin = strings.NewReader(string(input))
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	// The helper reports sanitized errors as JSON; never log a page payload or its stderr.
	command.Stderr = io.Discard
	if err = command.Start(); err != nil {
		return errors.New("无法调用 Windows 桌面读取助手")
	}
	stream := bufio.NewReader(io.LimitReader(stdout, 64<<20))
	if prefix, _ := stream.Peek(3); bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		stream.Discard(3)
	}
	decoder := json.NewDecoder(stream)
	var readErr error
	for {
		var result desktopCaptureResult
		err = decoder.Decode(&result)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			readErr = errors.New("桌面读取助手返回了不完整的数据，下次同步重试")
			break
		}
		if err = accept(result); err != nil {
			readErr = err
			break
		}
	}
	if readErr != nil {
		command.Process.Kill()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return readErr
	}
	if ctx.Err() != nil {
		return errors.New("桌面补全达到本轮时间限制；已导入的分页保留，下次继续")
	}
	if waitErr != nil {
		return errors.New("桌面读取助手未能完成；常规缓存同步已保留")
	}
	return nil
}

func (i *Importer) importDesktopCapture(job desktopCaptureJob, result desktopCaptureResult, full bool) error {
	if result.ID != job.ID || result.Pages < 1 || result.Conversation == nil {
		return errors.New("桌面补全结果的会话标识或分页数无效")
	}
	c := result.Conversation
	id := str(first(c["conversation_id"], c["id"]))
	if id != job.ID {
		return errors.New("桌面补全正文的会话 ID 不一致")
	}
	page := obj(c["page_info"])
	if result.Complete && (page["has_previous_page"] == true || page["has_next_page"] == true) {
		return errors.New("桌面补全结果仍有未读取分页，不能标记完整")
	}
	if !result.Complete && result.Cursor == "" {
		return errors.New("未完成的桌面补全缺少继续读取的游标")
	}
	streaming := false
	missing := []string{}
	for _, value := range array(c["messages"]) {
		m := obj(value)
		md := obj(m["metadata"])
		if str(m["id"]) == str(c["current_node"]) && (str(m["status"]) == "in_progress" || md["is_complete"] == false) {
			streaming = true
		}
		role := str(obj(m["author"])["role"])
		if md["is_visually_hidden_from_conversation"] == true || !(role == "user" || role == "assistant" && str(m["channel"]) == "final") {
			continue
		}
		content := obj(m["content"])
		if strings.TrimSpace(textContent(content["parts"])+str(content["text"])+textContent(content["content"])) != "" || len(chatGPTUI(md)) > 0 {
			continue
		}
		missing = append(missing, "node:"+str(m["id"]))
	}
	c["echo_desktop_capture"] = M{"complete": result.Complete && !streaming, "pages": result.Pages, "streaming": streaming}
	raw := []byte(jsonText(c))
	if len(raw) > cacheReadLimit {
		return errors.New("桌面会话超过单次归档大小限制")
	}
	digest := hash(raw)
	generation, _ := strconv.ParseInt(digest[:15], 16, 64)
	i.Store.importMu.Lock()
	defer i.Store.importMu.Unlock()
	if !full {
		if _, e := one(i.Store.DB, "SELECT session_id FROM deleted_sessions WHERE session_id=?", "chatgpt:"+job.ID); e == nil {
			i.update(func(s *SyncStatus) { s.Skipped++ })
			return nil
		} else if !errors.Is(e, errNotFound) {
			return e
		}
	}
	path := "desktop-live::" + job.ID
	n, err := i.chatgptConversation(c, path, generation, full, "Windows", raw)
	if err != nil {
		return err
	}
	if _, err = i.Store.Write.Exec("INSERT INTO files(path,session_id,head_hash,mode,last_sync) VALUES (?,?,?,'desktop_live',?) ON CONFLICT(path) DO UPDATE SET head_hash=excluded.head_hash,last_sync=excluded.last_sync,missing=0", path, "chatgpt:"+job.ID, digest, now()); err != nil {
		return err
	}
	updated := stamp(c["update_time"])
	if updated < job.updated {
		updated = job.updated
	}
	missingBodies := 0
	for _, identity := range missing {
		if _, e := one(i.Store.DB, "SELECT id FROM messages WHERE session_id=? AND identity=?", "chatgpt:"+job.ID, identity); errors.Is(e, errNotFound) {
			missingBodies++
		} else if e != nil {
			return e
		}
	}
	if missingBodies > 0 {
		if _, err = i.Store.Write.Exec("UPDATE sessions SET metadata=json_set(metadata,'$.desktop_capture_complete',json('false'),'$.desktop_missing_bodies',?) WHERE id=?", missingBodies, "chatgpt:"+job.ID); err != nil {
			return err
		}
	}
	row, err := one(i.Store.DB, "SELECT message_count,updated FROM sessions WHERE id=?", "chatgpt:"+job.ID)
	if err != nil {
		return err
	}
	if updated < number(row["updated"]) {
		updated = number(row["updated"])
	}
	if err = i.saveDesktopCheckpoint(job, desktopCheckpoint{Updated: updated, Attempt: now(), Complete: result.Complete, Cursor: result.Cursor, Streaming: streaming, MissingBodies: missingBodies}); err != nil {
		return err
	}
	i.update(func(s *SyncStatus) {
		s.Imported += n
		if s.SessionID == "chatgpt:"+job.ID {
			s.Done = 1
			s.SessionResult = &sessionSyncResult{Complete: result.Complete, Pages: result.Pages, Messages: int(integer(row["message_count"])), Streaming: streaming, MissingBodies: missingBodies}
		}
		if result.Complete && !streaming && missingBodies == 0 {
			s.DesktopCaptured++
			if s.DesktopPending > 0 {
				s.DesktopPending--
			}
			warnings := s.Warnings[:0]
			for _, warning := range s.Warnings {
				if warning.Path != job.title || warning.Error != desktopPartialCacheWarning {
					warnings = append(warnings, warning)
				}
			}
			s.Warnings = warnings
		}
	})
	if streaming {
		i.issue(job.title, errors.New("回答仍在输出；本次已保存可读取内容，后续同步会继续检查正文"), true)
	}
	if missingBodies > 0 {
		i.issue(job.title, fmt.Errorf("分页已读取，但 Desktop 接口仍有 %d 条提问 / 最终回答未返回正文；可稍后重新同步或导入官方导出", missingBodies), true)
	}
	return nil
}

func (i *Importer) captureDesktopChats(full bool) {
	settings, err := i.desktopCaptureSettings()
	if err != nil {
		i.issue("桌面同步与历史补全", err, true)
		return
	}
	if settings["enabled"] != true {
		return
	}
	i.update(func(s *SyncStatus) { s.Stage = "chatgpt_desktop_live"; s.File = "发现最近 Chat 会话" })
	recent := i.discoverDesktopChats()
	jobs, err := i.desktopCaptureCandidates(full, now(), recent...)
	if err != nil {
		i.issue("桌面同步与历史补全", err, true)
		return
	}
	if len(jobs) == 0 {
		return
	}
	i.update(func(s *SyncStatus) { s.Stage = "chatgpt_desktop_live"; s.DesktopPending = len(jobs) })
	if len(jobs) > desktopCaptureBatch {
		jobs = jobs[:desktopCaptureBatch]
	}
	i.captureDesktopJobs(jobs, full, false)
}

func (i *Importer) discoverDesktopChats() []desktopRecentConversation {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	reader := i.desktopRead
	if reader == nil {
		reader = readDesktopCapture
	}
	var recent []desktopRecentConversation
	received := false
	err := reader(ctx, nil, func(result desktopCaptureResult) error {
		received = true
		switch result.Kind {
		case "recent":
			if len(result.Recent) > 20 {
				return errors.New("最近会话列表超过本轮读取范围")
			}
			recent = result.Recent
		case "unavailable":
			return desktopUnavailable(result.Error)
		default:
			return fmt.Errorf("最近会话列表读取失败（HTTP %d）；本轮继续同步磁盘缓存和已收录会话", result.Status)
		}
		return nil
	})
	if err == nil && !received {
		err = errors.New("桌面未返回最近会话列表；本轮继续同步磁盘缓存和已收录会话")
	}
	if err != nil {
		i.issue("发现最近 Chat 会话", err, true)
		return nil
	}
	return recent
}

func (i *Importer) captureDesktopJobs(jobs []desktopCaptureJob, full, targeted bool) {
	selected := map[string]desktopCaptureJob{}
	for _, job := range jobs {
		selected[job.ID] = job
	}
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
	defer cancel()
	reader := i.desktopRead
	if reader == nil {
		reader = readDesktopCapture
	}
	received := map[string]bool{}
	err := reader(ctx, jobs, func(result desktopCaptureResult) error {
		if result.Kind == "unavailable" {
			i.issue("桌面同步与历史补全", desktopUnavailable(result.Error), !targeted)
			for _, job := range jobs {
				received[job.ID] = true
			}
			return nil
		}
		job, ok := selected[result.ID]
		if !ok {
			return errors.New("桌面读取助手返回了未请求的会话")
		}
		received[result.ID] = true
		i.update(func(s *SyncStatus) { s.File = job.title })
		if result.Kind == "error" {
			if err := i.saveDesktopCheckpoint(job, desktopCheckpoint{Updated: job.updated, Attempt: now(), RetryAfter: now() + 30*60, Cursor: job.Cursor}); err != nil {
				return err
			}
			i.issue(job.title, fmt.Errorf("桌面分页读取暂未完成（HTTP %d）；请确认当前 Desktop 登录的是此会话所属账号", result.Status), !targeted)
			return nil
		}
		if result.Kind != "conversation" {
			return errors.New("桌面读取助手返回了未知结果类型")
		}
		return i.importDesktopCapture(job, result, full)
	})
	if err != nil {
		i.issue("桌面同步与历史补全", err, !targeted)
	}
	if targeted && err == nil && !received[jobs[0].ID] {
		i.issue(jobs[0].title, errors.New("桌面读取未返回此会话，未能重新同步；请重试"), false)
	}
}
