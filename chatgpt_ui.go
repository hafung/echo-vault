package main

import (
	"encoding/json"
	"errors"
)

// Keep the server's resolved data with the source. DIL and OpenUI Lang are
// different languages; the reader renders DIL without evaluating its JavaScript.
func chatGPTUI(metadata M) M {
	ui := M{}
	for _, key := range []string{"model_dil_v2", "genui_components", "content_references", "search_result_groups"} {
		if value := metadata[key]; value != nil {
			ui[key] = value
		}
	}
	return ui
}

// Recover data discarded by older importers from the archive itself, including
// sources that have since disappeared. Only message metadata is changed.
func (s *Store) recoverChatGPTUI() error {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	if row, err := one(s.DB, "SELECT value FROM settings WHERE key='chatgpt_ui_version'"); err == nil && str(row["value"]) == "1" {
		return nil
	} else if err != nil && !errors.Is(err, errNotFound) {
		return err
	}
	var last int64
	for {
		chunks, err := query(s.DB, "SELECT id,session_id,payload FROM raw_chunks WHERE id>? AND session_id IN (SELECT id FROM sessions WHERE provider='ChatGPT') ORDER BY id LIMIT 8", last)
		if err != nil {
			return err
		}
		if len(chunks) == 0 {
			break
		}
		for _, chunk := range chunks {
			last = integer(chunk["id"])
			raw, err := s.rawBody(chunk["payload"].([]byte))
			if err != nil {
				return err
			}
			var conversation M
			if json.Unmarshal([]byte(raw), &conversation) != nil {
				continue
			}
			messages := array(conversation["messages"])
			for nid, value := range obj(conversation["mapping"]) {
				message := obj(obj(value)["message"])
				// Mapping IDs are the importer identity, even if message.id differs.
				messages = append(messages, M{"id": nid, "metadata": message["metadata"]})
			}
			tx, err := s.Write.Begin()
			if err != nil {
				return err
			}
			for _, value := range messages {
				message := obj(value)
				ui := chatGPTUI(obj(message["metadata"]))
				if len(obj(ui["model_dil_v2"])) == 0 {
					continue
				}
				row, err := one(tx, "SELECT id,metadata FROM messages WHERE session_id=? AND identity=?", chunk["session_id"], "node:"+str(message["id"]))
				if errors.Is(err, errNotFound) {
					continue
				}
				if err != nil {
					tx.Rollback()
					return err
				}
				var metadata M
				if err = json.Unmarshal([]byte(str(row["metadata"])), &metadata); err != nil {
					tx.Rollback()
					return err
				}
				// A current importer may already have newer data than the raw chunk.
				if metadata["chatgpt_ui"] != nil && metadata["chatgpt_ui_recovered"] != true {
					continue
				}
				metadata["chatgpt_ui"], metadata["chatgpt_ui_recovered"] = ui, true
				if _, err = tx.Exec("UPDATE messages SET metadata=? WHERE id=?", jsonText(metadata), row["id"]); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err = tx.Commit(); err != nil {
				return err
			}
		}
	}
	_, err := s.Write.Exec("INSERT INTO settings(key,value) VALUES ('chatgpt_ui_version','1') ON CONFLICT(key) DO UPDATE SET value=excluded.value")
	return err
}
