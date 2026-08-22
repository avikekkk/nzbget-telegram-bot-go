package nzbget

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveNZBGet exercises the read-only calls against a real server. It is
// skipped unless NZBGET_BASE is set, so the normal test run stays offline.
//
// Nothing here mutates the queue: append and editqueue are deliberately left
// out so running this against a production instance is harmless.
func TestLiveNZBGet(t *testing.T) {
	base := os.Getenv("NZBGET_BASE")
	if base == "" {
		t.Skip("NZBGET_BASE not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := New(base)

	version, err := client.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version == "" {
		t.Error("Version returned an empty string")
	}
	t.Logf("version: %s", version)

	status, err := client.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.TotalDiskSpace < 0 || status.FreeDiskSpace < 0 {
		t.Errorf("negative disk space: %+v", status)
	}
	if status.FreeDiskSpace > status.TotalDiskSpace && status.TotalDiskSpace > 0 {
		t.Errorf("free exceeds total: %+v", status)
	}
	t.Logf("status: uptime=%ds rate=%dB/s disk=%d free=%d",
		status.UpTimeSec, status.DownloadRate, status.TotalDiskSpace, status.FreeDiskSpace)

	groups, err := client.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	for _, group := range groups {
		if group.NZBID == 0 {
			t.Errorf("group with no NZBID: %+v", group)
		}
		if group.DownloadedSize < 0 || group.FileSize < 0 {
			t.Errorf("negative size on %d: downloaded=%d total=%d",
				group.NZBID, group.DownloadedSize, group.FileSize)
		}
		t.Logf("group %d %s %s %d/%d health=%d",
			group.NZBID, group.Status, group.NZBName,
			group.DownloadedSize, group.FileSize, group.Health)
	}

	history, err := client.History(ctx, false)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	for _, item := range history {
		if item.NZBID == 0 {
			t.Errorf("history item with no NZBID: %+v", item)
		}
	}
	t.Logf("history: %d item(s)", len(history))
}
