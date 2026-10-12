package main

import "context"

// Full scans only clear a deletion marker when they encounter that session.
// Unavailable sources therefore keep their deletion markers for later syncs.
func importSession(db DBQuery, sid string, full bool) (bool, error) {
	if full {
		_, err := db.Exec("DELETE FROM deleted_sessions WHERE session_id=?", sid)
		return err == nil, err
	}
	var deleted bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM deleted_sessions WHERE session_id=?)", sid).Scan(&deleted)
	return !deleted, err
}

func (s *Store) deleteSession(ctx context.Context, sid string) error {
	s.importMu.Lock()
	defer s.importMu.Unlock()
	tx, err := s.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = one(tx, "SELECT id FROM sessions WHERE id=?", sid); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO deleted_sessions(session_id,deleted) VALUES (?,?) ON CONFLICT(session_id) DO UPDATE SET deleted=excluded.deleted", sid, now()); err != nil {
		return err
	}
	for _, table := range []string{"search_long", "search_short", "dialogue_long", "dialogue_short"} {
		if _, err = tx.Exec("DELETE FROM "+table+" WHERE rowid IN (SELECT id FROM messages WHERE session_id=?)", sid); err != nil {
			return err
		}
	}
	// Remove exclusive attachments and raw blobs, retaining payloads shared by other sessions.
	if _, err = tx.Exec(`DELETE FROM assets WHERE id IN (
		SELECT a.value FROM messages m,json_each(m.metadata,'$.assets') a WHERE m.session_id=? AND a.type='text'
		EXCEPT
		SELECT a.value FROM messages m,json_each(m.metadata,'$.assets') a WHERE m.session_id<>? AND a.type='text'
	)`, sid, sid); err != nil {
		return err
	}
	if _, err = tx.Exec(`DELETE FROM raw_blobs WHERE digest IN (
		SELECT substr(CAST(payload AS TEXT),?) FROM raw_chunks WHERE session_id=? AND substr(payload,1,?)=?
		EXCEPT
		SELECT substr(CAST(payload AS TEXT),?) FROM raw_chunks WHERE session_id IS NOT ? AND substr(payload,1,?)=?
	)`, len(rawReference)+1, sid, len(rawReference), []byte(rawReference), len(rawReference)+1, sid, len(rawReference), []byte(rawReference)); err != nil {
		return err
	}
	for _, q := range []string{
		"DELETE FROM messages WHERE session_id=?",
		"DELETE FROM session_visibility WHERE session_id=?",
		"DELETE FROM raw_chunks WHERE session_id=?",
		"DELETE FROM files WHERE session_id=?",
		"DELETE FROM db_items WHERE thread_id=?",
		"DELETE FROM sessions WHERE id=?",
	} {
		if _, err = tx.Exec(q, sid); err != nil {
			return err
		}
	}
	return tx.Commit()
}
