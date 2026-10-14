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
	Updated    float64 `json:"updated"`
	Attempt    float64 `json:"attempt"`
	RetryAfter float64 `json:"retry_after"`
	Complete   bool    `json:"complete"`
	Cursor     string  `json:"cursor,omitempty"`
}

type desktopCaptureJob struct {
	ID         string `json:"id"`
	Cursor     string `json:"cursor,omitempty"`
	title      string
	updated    float64
	checkpoint desktopCheckpoint
}

type desktopCaptureResult struct {
	Kind         string `json:"kind"`
	ID           string `json:"id"`
	Complete     bool   `json:"complete"`
	Cursor       string `json:"cursor"`
	Pages        int    `json:"pages"`
	Status       int    `json:"status"`
	Error        string `json:"error"`
	Conversation M      `json:"conversation"`
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
		return errors.New("桌面主动补全目前支持 Windows 和 WSL")
	}
	value := "0"
	if enabled {
		value = "1"
	}
	_, err := i.Store.Write.Exec("INSERT INTO settings(key,value) VALUES ('desktop_capture',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", value)
	return err
}

func (i *Importer) desktopCaptureCandidates(full bool, timestamp float64) ([]desktopCaptureJob, error) {
	rows, err := query(i.Store.DB, `SELECT s.id,s.title,s.updated,c.value AS checkpoint FROM sessions s
		LEFT JOIN settings c ON c.key=? || s.id
		WHERE s.provider='ChatGPT' AND s.platform='Windows' AND json_extract(s.metadata,'$.desktop_cache')=1`, desktopCheckpointPrefix)
	if err != nil {
		return nil, err
	}
	jobs := []desktopCaptureJob{}
	for _, row := range rows {
		id := strings.TrimPrefix(str(row["id"]), "chatgpt:")
		if !desktopConversationID.MatchString(id) {
			continue
		}
		var checkpoint desktopCheckpoint
		json.Unmarshal([]byte(str(row["checkpoint"])), &checkpoint)
		updated := number(row["updated"])
		if !full && (checkpoint.RetryAfter > timestamp || checkpoint.Complete && checkpoint.Updated >= updated && timestamp-checkpoint.Attempt < 24*60*60) {
			continue
		}
		cursor := checkpoint.Cursor
		if full || checkpoint.Updated != updated || checkpoint.Complete {
			cursor = ""
		}
		jobs = append(jobs, desktopCaptureJob{ID: id, Cursor: cursor, title: str(row["title"]), updated: updated, checkpoint: checkpoint})
	}
	sort.SliceStable(jobs, func(a, b int) bool {
		if jobs[a].checkpoint.Attempt == jobs[b].checkpoint.Attempt {
			return jobs[a].updated > jobs[b].updated
		}
		return jobs[a].checkpoint.Attempt < jobs[b].checkpoint.Attempt
	})
	return jobs, nil
}

func (i *Importer) saveDesktopCheckpoint(job desktopCaptureJob, checkpoint desktopCheckpoint) error {
	_, err := i.Store.Write.Exec("INSERT INTO settings(key,value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", desktopCheckpointPrefix+"chatgpt:"+job.ID, jsonText(checkpoint))
	return err
}

func readDesktopCapture(ctx context.Context, jobs []desktopCaptureJob, accept func(desktopCaptureResult) error) error {
	input, err := json.Marshal(M{"port": desktopCapturePort, "jobs": jobs})
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
	c["echo_desktop_capture"] = M{"complete": result.Complete, "pages": result.Pages}
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
	if err = i.saveDesktopCheckpoint(job, desktopCheckpoint{Updated: updated, Attempt: now(), Complete: result.Complete, Cursor: result.Cursor}); err != nil {
		return err
	}
	i.update(func(s *SyncStatus) {
		s.Imported += n
		if result.Complete {
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
	return nil
}

func (i *Importer) captureDesktopChats(full bool) {
	settings, err := i.desktopCaptureSettings()
	if err != nil {
		i.issue("桌面主动补全", err, true)
		return
	}
	if settings["enabled"] != true {
		return
	}
	jobs, err := i.desktopCaptureCandidates(full, now())
	if err != nil {
		i.issue("桌面主动补全", err, true)
		return
	}
	if len(jobs) == 0 {
		return
	}
	i.update(func(s *SyncStatus) { s.Stage = "chatgpt_desktop_live"; s.DesktopPending = len(jobs) })
	if len(jobs) > desktopCaptureBatch {
		jobs = jobs[:desktopCaptureBatch]
	}
	selected := map[string]desktopCaptureJob{}
	for _, job := range jobs {
		selected[job.ID] = job
	}
	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Second)
	defer cancel()
	err = readDesktopCapture(ctx, jobs, func(result desktopCaptureResult) error {
		if result.Kind == "unavailable" {
			i.issue("桌面主动补全", errors.New("请通过 Start ChatGPT Enhanced.cmd 启动已登录的 Desktop；本轮已完成常规缓存同步"), true)
			return nil
		}
		job, ok := selected[result.ID]
		if !ok {
			return errors.New("桌面读取助手返回了未请求的会话")
		}
		i.update(func(s *SyncStatus) { s.File = job.title })
		if result.Kind == "error" {
			if err := i.saveDesktopCheckpoint(job, desktopCheckpoint{Updated: job.updated, Attempt: now(), RetryAfter: now() + 30*60, Cursor: job.Cursor}); err != nil {
				return err
			}
			i.issue(job.title, fmt.Errorf("桌面分页读取暂未完成（HTTP %d）；已保存内容保留，稍后重试", result.Status), true)
			return nil
		}
		if result.Kind != "conversation" {
			return errors.New("桌面读取助手返回了未知结果类型")
		}
		return i.importDesktopCapture(job, result, full)
	})
	if err != nil {
		i.issue("桌面主动补全", err, true)
	}
}
