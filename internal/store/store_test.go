package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "bot.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAuthChats(t *testing.T) {
	db := open(t)

	if chats, err := db.LoadAuthChats(); err != nil || len(chats) != 0 {
		t.Fatalf("LoadAuthChats on a fresh database = %v, %v", chats, err)
	}

	if err := db.AddAuthChat(-1001234567890); err != nil {
		t.Fatalf("AddAuthChat: %v", err)
	}
	// Authorizing twice must not create a second row.
	if err := db.AddAuthChat(-1001234567890); err != nil {
		t.Fatalf("AddAuthChat again: %v", err)
	}

	chats, err := db.LoadAuthChats()
	if err != nil || len(chats) != 1 || chats[0] != -1001234567890 {
		t.Fatalf("LoadAuthChats = %v, %v", chats, err)
	}

	if err := db.RemoveAuthChat(-1001234567890); err != nil {
		t.Fatalf("RemoveAuthChat: %v", err)
	}
	if chats, _ := db.LoadAuthChats(); len(chats) != 0 {
		t.Fatalf("chat survived removal: %v", chats)
	}
}

func TestTasks(t *testing.T) {
	db := open(t)

	if err := db.AddTask(7, 111); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if err := db.AddTask(8, 222); err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	// Re-adding an NZBID reassigns it rather than failing on the primary key.
	if err := db.AddTask(7, 333); err != nil {
		t.Fatalf("AddTask replace: %v", err)
	}

	tasks, err := db.LoadTasks()
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if tasks[7] != 333 || tasks[8] != 222 {
		t.Fatalf("tasks = %v", tasks)
	}

	if err := db.RemoveTasks(nil); err != nil {
		t.Fatalf("RemoveTasks(nil): %v", err)
	}
	if err := db.RemoveTasks([]int64{7, 8, 9}); err != nil {
		t.Fatalf("RemoveTasks: %v", err)
	}
	if tasks, _ := db.LoadTasks(); len(tasks) != 0 {
		t.Fatalf("tasks survived removal: %v", tasks)
	}
}
