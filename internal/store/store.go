// Package store persists the two pieces of state that must survive a restart:
// which chats are authorized, and which user queued which NZBID.
package store

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite" // pure-Go driver, so the bot builds without cgo
)

// Store is the bot's SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	schema := []string{
		"CREATE TABLE IF NOT EXISTS auth_chats (chat_id INTEGER PRIMARY KEY)",
		"CREATE TABLE IF NOT EXISTS tasks (nzb_id INTEGER PRIMARY KEY, user_id INTEGER)",
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// LoadAuthChats returns every chat authorized through /auth.
func (s *Store) LoadAuthChats() ([]int64, error) {
	rows, err := s.db.Query("SELECT chat_id FROM auth_chats")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chats []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		chats = append(chats, id)
	}
	return chats, rows.Err()
}

// AddAuthChat authorizes a chat.
func (s *Store) AddAuthChat(chatID int64) error {
	_, err := s.db.Exec("INSERT OR IGNORE INTO auth_chats (chat_id) VALUES (?)", chatID)
	return err
}

// RemoveAuthChat revokes a chat's authorization.
func (s *Store) RemoveAuthChat(chatID int64) error {
	_, err := s.db.Exec("DELETE FROM auth_chats WHERE chat_id = ?", chatID)
	return err
}

// LoadTasks returns the NZBID to owner mapping.
//
// Ownership has to survive a restart: /cancel checks it, so holding it only in
// memory left every download unowned and cancellable by admins alone.
func (s *Store) LoadTasks() (map[int64]int64, error) {
	rows, err := s.db.Query("SELECT nzb_id, user_id FROM tasks")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tasks := map[int64]int64{}
	for rows.Next() {
		var nzbID, userID int64
		if err := rows.Scan(&nzbID, &userID); err != nil {
			return nil, err
		}
		tasks[nzbID] = userID
	}
	return tasks, rows.Err()
}

// AddTask records who queued an NZBID.
func (s *Store) AddTask(nzbID, userID int64) error {
	_, err := s.db.Exec("INSERT OR REPLACE INTO tasks (nzb_id, user_id) VALUES (?, ?)", nzbID, userID)
	return err
}

// RemoveTasks forgets the given NZBIDs.
func (s *Store) RemoveTasks(nzbIDs []int64) error {
	if len(nzbIDs) == 0 {
		return nil
	}
	args := make([]any, len(nzbIDs))
	for i, id := range nzbIDs {
		args[i] = id
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(nzbIDs)), ",")
	_, err := s.db.Exec("DELETE FROM tasks WHERE nzb_id IN ("+placeholders+")", args...)
	return err
}
