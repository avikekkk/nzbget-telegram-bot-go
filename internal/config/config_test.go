package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLogFileLayout pins where the log lands. The name is what a group sees
// when /logs uploads it, next to the sibling bot's clonebot.log.
func TestLogFileLayout(t *testing.T) {
	dir := t.TempDir()
	for key, value := range map[string]string{
		"API_ID": "1", "API_HASH": "x", "BOT_TOKEN": "x",
		"ADMINS": "[1]", "AUTH_CHATS": "[1]", "NZBGET_BASE": "http://x",
		"DATA_DIR": dir,
	} {
		t.Setenv(key, value)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(dir, "logs", "nzbgetbot.log")
	if cfg.LogFile != want {
		t.Errorf("LogFile = %q, want %q", cfg.LogFile, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("DATA_DIR not created: %v", err)
	}
}
