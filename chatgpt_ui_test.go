package main

import (
	"context"
	"encoding/json"
	"net/url"
	"testing"
)

func TestChatGPTUIImportRecoveryAndOlderSnapshot(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	dil := M{"fallbackMarkdown": "第二选择：声音克隆", "constants": M{"0": "第二选择"}, "appData": M{"opGenui": M{"componentResults": M{"image-id": M{"componentName": "AsyncImage", "state": M{"images": []any{M{"content_url": "https://images.openai.com/example.png"}}}}}}}}
	message := M{"id": "answer", "author": M{"role": "assistant"}, "content": M{"content_type": "text", "parts": []any{"<box>\n  **第二选择：**\n\n  声音克隆\n</box>"}}, "metadata": M{"model_dil_v2": dil}, "create_time": 2}
	conversation := M{"id": "ui", "current_node": "answer", "mapping": M{"answer": M{"message": message}}}
	_, err := i.chatgptConversation(conversation, "removed-source", 1, false, "云端导出", nil)
	check(t, err)
	page, err := s.messages(context.Background(), "chatgpt:ui", url.Values{})
	check(t, err)
	metadata := obj(page["items"].([]M)[0]["metadata"])
	if jsonText(obj(metadata["chatgpt_ui"])["model_dil_v2"]) != jsonText(dil) {
		t.Fatal("resolved DIL data was discarded")
	}
	// Simulate an archive made with the old importer and a deleted source.
	delete(metadata, "chatgpt_ui")
	_, err = s.Write.Exec("UPDATE messages SET metadata=? WHERE session_id='chatgpt:ui'", jsonText(metadata))
	check(t, err)
	check(t, s.recoverChatGPTUI())
	check(t, s.recoverChatGPTUI())
	row, err := one(s.DB, "SELECT metadata FROM messages WHERE session_id='chatgpt:ui'")
	check(t, err)
	check(t, json.Unmarshal([]byte(str(row["metadata"])), &metadata))
	if jsonText(obj(metadata["chatgpt_ui"])["model_dil_v2"]) != jsonText(dil) {
		t.Fatal("archived original did not recover UI data")
	}
	message["metadata"] = M{}
	_, err = i.chatgptConversation(conversation, "older-snapshot", 2, false, "云端导出", nil)
	check(t, err)
	row, err = one(s.DB, "SELECT metadata FROM messages WHERE session_id='chatgpt:ui'")
	check(t, err)
	check(t, json.Unmarshal([]byte(str(row["metadata"])), &metadata))
	if len(obj(metadata["chatgpt_ui"])) == 0 {
		t.Fatal("older snapshot erased resolved data")
	}
}

func TestChatGPTUIFallbackOnlyMessage(t *testing.T) {
	s := testStore(t)
	i := newImporter(s)
	_, err := i.chatgptConversation(M{"id": "fallback", "messages": []any{M{"id": "answer", "author": M{"role": "assistant"}, "content": M{}, "metadata": M{"model_dil_v2": M{"fallbackMarkdown": "保留的回答"}}}}}, "cache", 1, false, "Windows", nil)
	check(t, err)
	page, err := s.messages(context.Background(), "chatgpt:fallback", url.Values{})
	check(t, err)
	items := page["items"].([]M)
	if len(items) != 1 || str(items[0]["body"]) != "保留的回答" {
		t.Fatal("fallback-only answer was dropped")
	}
}
