package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func liveTestResult(id string, complete bool, cursor string, messages ...any) desktopCaptureResult {
	return desktopCaptureResult{Kind: "conversation", ID: id, Complete: complete, Cursor: cursor, Pages: 1, Conversation: M{"conversation_id": id, "title": "分页补全", "create_time": 100, "update_time": 110, "current_node": "a", "messages": messages, "page_info": M{"has_previous_page": !complete, "has_next_page": false}}}
}

func TestDesktopLiveResumesAndMergesWithoutReplacingRecentHistory(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	job := desktopCaptureJob{ID: "live-test", updated: 110, title: "分页补全"}
	check(t, i.importDesktopCapture(job, liveTestResult(job.ID, false, "cursor-older", cacheMessage("q", "user", "新问题", 109), cacheMessage("a", "assistant", "新回答", 110)), false))
	jobs, err := i.desktopCaptureCandidates(false, now())
	check(t, err)
	if len(jobs) != 1 || jobs[0].Cursor != "cursor-older" {
		t.Fatalf("incomplete capture lost its cursor: %v", jobs)
	}
	check(t, i.importDesktopCapture(jobs[0], liveTestResult(job.ID, true, "", cacheMessage("old-q", "user", "历史问题", 101), cacheMessage("old-a", "assistant", "历史回答", 102), cacheMessage("a", "assistant", "新回答", 110)), false))
	row, err := one(s.DB, "SELECT message_count,metadata FROM sessions WHERE id=?", "chatgpt:"+job.ID)
	check(t, err)
	if integer(row["message_count"]) != 4 {
		t.Fatal("older pages replaced newer messages")
	}
	var metadata M
	check(t, json.Unmarshal([]byte(str(row["metadata"])), &metadata))
	if metadata["desktop_capture_complete"] != true || metadata["cache_partial"] != false || metadata["current_node"] != "a" {
		t.Fatalf("complete history has wrong metadata: %v", metadata)
	}
	jobs, err = i.desktopCaptureCandidates(false, now())
	check(t, err)
	if len(jobs) != 0 {
		t.Fatal("unchanged completed history was fetched again")
	}
	jobs, err = i.desktopCaptureCandidates(false, now()+25*60*60)
	check(t, err)
	if len(jobs) != 1 || jobs[0].Cursor != "" {
		t.Fatal("periodic recovery did not start from the latest page")
	}
	_, err = s.Write.Exec("UPDATE sessions SET updated=111 WHERE id=?", "chatgpt:"+job.ID)
	check(t, err)
	jobs, err = i.desktopCaptureCandidates(false, now())
	check(t, err)
	if len(jobs) != 1 || jobs[0].Cursor != "" {
		t.Fatal("an updated conversation was skipped")
	}
}

func TestDesktopLiveRejectsIncompleteSuccessAndWrongIdentity(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	job := desktopCaptureJob{ID: "expected"}
	wrong := liveTestResult("different", true, "", cacheMessage("q", "user", "错配正文", 100))
	if i.importDesktopCapture(job, wrong, false) == nil {
		t.Fatal("wrong identity accepted")
	}
	invalid := liveTestResult(job.ID, true, "", cacheMessage("q", "user", "部分正文", 100))
	invalid.Conversation["page_info"] = M{"has_previous_page": true}
	if i.importDesktopCapture(job, invalid, false) == nil {
		t.Fatal("partial source marked complete")
	}
	invalid = liveTestResult(job.ID, false, "", cacheMessage("q", "user", "部分正文", 100))
	if i.importDesktopCapture(job, invalid, false) == nil {
		t.Fatal("partial source without a resume cursor accepted")
	}
	if count(t, s, "messages") != 0 || count(t, s, "settings") > 1 {
		t.Fatal("rejected result left imported records/checkpoints")
	}
}

func TestDesktopLiveDeletionDuringNetworkReadDoesNotResurrect(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	job := desktopCaptureJob{ID: "delete-test", updated: 110}
	result := liveTestResult(job.ID, true, "", cacheMessage("q", "user", "待删除", 109), cacheMessage("a", "assistant", "回答", 110))
	check(t, i.importDesktopCapture(job, result, false))
	check(t, s.deleteSession(context.Background(), "chatgpt:"+job.ID))
	check(t, i.importDesktopCapture(job, result, false))
	if count(t, s, "sessions") != 0 || count(t, s, "messages") != 0 || count(t, s, "files") != 0 {
		t.Fatal("network response resurrected a deleted conversation")
	}
	check(t, i.importDesktopCapture(job, result, true))
	if count(t, s, "messages") != 2 {
		t.Fatal("explicit full import could not restore the available conversation")
	}
}

func TestDesktopLiveCompleteStatusSurvivesOldCachedPages(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	job := desktopCaptureJob{ID: "complete-test", updated: 110}
	check(t, i.importDesktopCapture(job, liveTestResult(job.ID, true, "", cacheMessage("q", "user", "问题", 109), cacheMessage("a", "assistant", "最新回答", 110)), false))
	older := M{"conversation_id": job.ID, "title": "分页补全", "update_time": 110, "messages": []any{cacheMessage("old", "user", "旧分页", 101)}, "page_info": M{"has_previous_page": false, "has_next_page": true}}
	_, err := i.chatgptConversation(older, "cache", 1, false, "Windows", []byte(jsonText(older)))
	check(t, err)
	row, err := one(s.DB, "SELECT metadata FROM sessions WHERE id=?", "chatgpt:"+job.ID)
	check(t, err)
	var metadata M
	check(t, json.Unmarshal([]byte(str(row["metadata"])), &metadata))
	if metadata["cache_partial"] != false || metadata["current_node"] != "a" {
		t.Fatal("an old page made completed history partial or changed its current node", metadata)
	}
}

func TestDesktopCaptureDefaultsOffAndFitsWindowsCommandLine(t *testing.T) {
	i := newImporter(testStore(t))
	settings, err := i.desktopCaptureSettings()
	check(t, err)
	if settings["enabled"] != false {
		t.Fatal("new archives must not enable live Desktop reads implicitly")
	}
	if len(encodedPowerShell(desktopCaptureScript))+200 >= 32767 {
		t.Fatal("embedded helper exceeds the Windows command line limit")
	}
}

func TestDesktopLiveFailedAccountReadsDoNotStarveOtherConversations(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	for _, id := range []string{"unavailable-account", "next-chat"} {
		job := desktopCaptureJob{ID: id, updated: 110}
		check(t, i.importDesktopCapture(job, liveTestResult(id, false, "continue", cacheMessage("q", "user", "问题", 109), cacheMessage("a", "assistant", "回答", 110)), false))
	}
	check(t, i.saveDesktopCheckpoint(desktopCaptureJob{ID: "unavailable-account"}, desktopCheckpoint{Updated: 110, Attempt: now(), RetryAfter: now() + 1800, Cursor: "continue"}))
	jobs, err := i.desktopCaptureCandidates(false, now())
	check(t, err)
	if len(jobs) != 1 || jobs[0].ID != "next-chat" {
		t.Fatal("a failed conversation blocked other work", jobs)
	}
	jobs, err = i.desktopCaptureCandidates(false, now()+1801)
	check(t, err)
	if len(jobs) != 2 {
		t.Fatal("failed conversation was never retried")
	}
}

// Optional end-to-end verification against an explicitly started localhost
// Desktop inspector. The archive is isolated and output contains counts only.
func TestDesktopLiveReadProbe(t *testing.T) {
	id := os.Getenv("ECHO_DESKTOP_LIVE_TEST_ID")
	if id == "" {
		t.Skip("set ECHO_DESKTOP_LIVE_TEST_ID with Desktop enhanced mode open")
	}
	if !desktopConversationID.MatchString(id) {
		t.Fatal("invalid probe ID")
	}
	i := newImporter(testStore(t))
	job := desktopCaptureJob{ID: id}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	complete := false
	check(t, readDesktopCapture(ctx, []desktopCaptureJob{job}, func(result desktopCaptureResult) error {
		if result.Kind != "conversation" {
			return &liveProbeError{result.Kind, result.Status}
		}
		complete = result.Complete
		return i.importDesktopCapture(job, result, false)
	}))
	row, err := one(i.Store.DB, "SELECT message_count,total_count FROM sessions WHERE id=?", "chatgpt:"+id)
	check(t, err)
	t.Logf("Desktop live read: complete=%v dialogue=%d records=%d", complete, integer(row["message_count"]), integer(row["total_count"]))
	if !complete || integer(row["message_count"]) == 0 {
		t.Fatal("live conversation was not completed")
	}
}

type liveProbeError struct {
	kind   string
	status int
}

func (e *liveProbeError) Error() string { return "Desktop probe returned " + strings.TrimSpace(e.kind) }
