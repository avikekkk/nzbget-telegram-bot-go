package bot

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/config"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/nzbget"
)

// liveBot builds a bot wired to the real NZBGet and NZBHydra, without a
// Telegram connection. Only the handler internals that do not touch Telegram
// can be exercised through it.
func liveBot(t *testing.T) *Bot {
	t.Helper()

	if os.Getenv("NZBGET_BASE") == "" {
		t.Skip("NZBGET_BASE not set")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if cfg.NZBHydraAPIURL == "" {
		t.Skip("NZBHYDRA_API_URL not set")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(cfg, logger, nil, nzbget.New(cfg.NZBGetBase))
}

// TestLiveSearch runs a real NZBHydra search and checks that every result can
// be turned back into the ID and site the grab path needs.
func TestLiveSearch(t *testing.T) {
	b := liveBot(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	results, _, err := b.idx.Search(ctx, "ubuntu", "min")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Skip("no results for the probe query")
	}
	t.Logf("%d result(s)", len(results))

	for i, result := range results {
		if result.Title == "" {
			t.Errorf("result %d has no title", i)
		}
		// Every displayed ID must survive the round trip, or /grab rejects it.
		if _, site, err := b.decodeNZBID(result.GUID); err != nil {
			t.Errorf("result %d: decodeNZBID: %v", i, err)
		} else if site != "nzbhydra" {
			t.Errorf("result %d: site = %q", i, site)
		}
		if i < 3 {
			t.Logf("  %s | %s | Age: %s", result.Title, result.SizeText, result.Age)
		}
	}

	// --mn asked for smallest first.
	for i := 1; i < len(results); i++ {
		if results[i].SizeBytes < results[i-1].SizeBytes {
			t.Errorf("results not sorted ascending at %d: %d < %d",
				i, results[i].SizeBytes, results[i-1].SizeBytes)
			break
		}
	}
}

// TestLiveGrabAndCancel walks the whole chain the bot walks for /grab: search,
// decode the displayed ID, hand it to NZBHydra's downloader integration, find
// the NZBID NZBGet assigned, then cancel it again.
//
// The queue is left as it was found: the download is removed at the end, and
// on any failure after the add.
func TestLiveGrabAndCancel(t *testing.T) {
	b := liveBot(t)

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	results, _, err := b.idx.Search(ctx, "ubuntu", "max")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Skip("no results for the probe query")
	}

	// Results are largest first. A small file finishes before it can be seen
	// downloading, and a size above 4 GiB is the case that needs NZBGet's
	// split Hi/Lo halves joined correctly, so prefer a large probe -- capped,
	// and cancelled long before it completes.
	const probeCap = 10 << 30
	probe := results[len(results)-1]
	for _, result := range results {
		if result.SizeBytes <= probeCap {
			probe = result
			break
		}
	}
	t.Logf("grabbing %q (%s)", probe.Title, probe.SizeText)

	resultID, site, err := b.decodeNZBID(probe.GUID)
	if err != nil {
		t.Fatalf("decodeNZBID: %v", err)
	}
	indexer, ok := b.idx.Get(site)
	if !ok {
		t.Fatalf("indexer %q not registered", site)
	}

	nzbID, err := b.appendInternal(ctx, resultID, indexer.APIURL)
	if err != nil {
		t.Fatalf("appendInternal: %v", err)
	}
	t.Logf("queued as NZBID %d", nzbID)

	// Always clean up, whatever the assertions below decide.
	defer func() {
		removed, err := b.nzb.EditQueue(context.Background(), "GroupFinalDelete", "", []int64{nzbID})
		if err != nil {
			t.Errorf("cleanup GroupFinalDelete: %v", err)
		}
		if _, err := b.nzb.EditQueue(context.Background(), "PostDelete", "", []int64{nzbID}); err != nil {
			t.Errorf("cleanup PostDelete: %v", err)
		}
		t.Logf("cleanup removed=%v", removed)

		groups, err := b.nzb.ListGroups(context.Background())
		if err != nil {
			t.Errorf("cleanup ListGroups: %v", err)
			return
		}
		for _, group := range groups {
			if group.NZBID == nzbID {
				t.Errorf("NZBID %d still queued after cleanup", nzbID)
			}
		}
	}()

	groups, err := b.nzb.ListGroups(ctx)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}

	var queued *nzbget.Group
	for i := range groups {
		if groups[i].NZBID == nzbID {
			queued = &groups[i]
			break
		}
	}
	if queued == nil {
		t.Fatalf("NZBID %d not found in the queue", nzbID)
	}
	if queued.NZBName == "" {
		t.Error("queued group has no NZBName, so /grab would report it unnamed")
	}
	t.Logf("queue entry: %s status=%s size=%d", queued.NZBName, queued.Status, queued.FileSize)

	// The status renderer runs over whatever NZBGet reports for it.
	status, err := b.nzb.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	rendered := renderStatus(groups, status.DownloadRate)
	if rendered == "" {
		t.Error("renderStatus produced nothing for a non-empty queue")
	}
	t.Logf("rendered status:\n%s", rendered)

	// A fresh add reports FETCHING until NZBGet has read the file list. Poll
	// often enough to catch DOWNLOADING before the transfer finishes, so the
	// download renderer is exercised against a real size and rate rather than
	// the placeholder zeroes.
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		time.Sleep(time.Second)

		groups, err := b.nzb.ListGroups(ctx)
		if err != nil {
			t.Fatalf("ListGroups: %v", err)
		}
		status, err := b.nzb.Status(ctx)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}

		var current *nzbget.Group
		for i := range groups {
			if groups[i].NZBID == nzbID {
				current = &groups[i]
				break
			}
		}
		if current == nil {
			t.Log("download left the queue before it could be observed downloading")
			return
		}
		if current.Status == "FETCHING" {
			continue
		}

		t.Logf("status=%s size=%d downloaded=%d rate=%d",
			current.Status, current.FileSize, current.DownloadedSize, status.DownloadRate)

		// The size NZBGet reports must match what the indexer advertised. A
		// mis-joined Hi/Lo pair shows up here as a wildly wrong or negative
		// total, which is exactly what a >4 GiB probe is for.
		if current.FileSize <= 0 {
			t.Errorf("FileSize = %d for a queued download", current.FileSize)
		}
		if current.DownloadedSize < 0 || current.DownloadedSize > current.FileSize {
			t.Errorf("DownloadedSize = %d, out of range for a total of %d",
				current.DownloadedSize, current.FileSize)
		}
		if ratio := float64(current.FileSize) / float64(probe.SizeBytes); ratio < 0.5 || ratio > 2 {
			t.Errorf("NZBGet reports %d bytes but the indexer advertised %d",
				current.FileSize, probe.SizeBytes)
		}

		t.Logf("rendered status:\n%s", renderStatus(groups, status.DownloadRate))
		if current.Status == "DOWNLOADING" {
			return
		}
	}
	t.Log("download stayed in FETCHING for the whole window")
}
