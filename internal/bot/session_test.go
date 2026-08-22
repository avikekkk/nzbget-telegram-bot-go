package bot

import (
	"sync"
	"testing"
)

// TestSessionRedactBeforePublish covers the window the background Telegraph
// publish opens: the reader can close the results, or the auto-redact timer
// can fire, while the page is still being created. The page must not then be
// adopted, or a keyboard button would point at a live list of NZB IDs for a
// search that was already redacted.
func TestSessionRedactBeforePublish(t *testing.T) {
	session := &searchSession{}

	if client, path := session.markRedacted(); client != nil || path != "" {
		t.Errorf("markRedacted on an unpublished session = (%v, %q), want nothing to blank",
			client, path)
	}
	if session.setTelegraph(nil, "https://telegra.ph/x", "x") {
		t.Error("a redacted session accepted a page published after the fact")
	}
	if url := session.telegraphURLValue(); url != "" {
		t.Errorf("telegraphURLValue = %q, want the button left off", url)
	}
}

// TestSessionPublishThenRedact is the ordinary order: the page is adopted, and
// redacting afterwards hands it back to be blanked.
func TestSessionPublishThenRedact(t *testing.T) {
	session := &searchSession{}

	if !session.setTelegraph(nil, "https://telegra.ph/x", "path-x") {
		t.Fatal("a fresh session refused its page")
	}
	if url := session.telegraphURLValue(); url != "https://telegra.ph/x" {
		t.Errorf("telegraphURLValue = %q", url)
	}
	if _, path := session.markRedacted(); path != "path-x" {
		t.Errorf("markRedacted path = %q, want the published page", path)
	}
}

// TestSessionPageIsConcurrencySafe exercises the fields the publish goroutine
// and the pagination callbacks share. It is the race detector that judges it.
func TestSessionPageIsConcurrencySafe(t *testing.T) {
	session := &searchSession{}

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); session.setPage(i) }()
		go func() { defer wg.Done(); _ = session.currentPage(); _ = session.telegraphURLValue() }()
	}
	wg.Add(1)
	go func() { defer wg.Done(); session.setTelegraph(nil, "u", "p") }()
	wg.Wait()
}
