package bot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/styling"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/config"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/indexers"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/nzbget"
)

// mustParseHTML runs a message through the same parser the send path uses.
//
// Pyrogram accepted mismatched tags like "<u><b>x</u></b>"; gotd rejects them,
// and the send fails at runtime rather than at build time. Every template the
// bot can produce is parsed here so that cannot reach a user again.
func mustParseHTML(t *testing.T, what, text string) {
	t.Helper()
	if err := styling.Perform(&entity.Builder{}, html.String(nil, text)); err != nil {
		t.Errorf("%s: %v\n---\n%s\n---", what, err, text)
	}
}

// TestParserRejectsMismatchedTags proves the check above has teeth: it is the
// exact markup the Python bot sent, which reached a user as "Error adding NZB
// file" even though the download had been queued.
func TestParserRejectsMismatchedTags(t *testing.T) {
	if err := styling.Perform(&entity.Builder{}, html.String(nil, "<u><b>NZB ADDED</u></b>")); err == nil {
		t.Error("parser accepted mismatched tags, so the other tests here prove nothing")
	}
}

func TestStaticMessagesParse(t *testing.T) {
	messages := map[string]string{
		"help":             helpText,
		"nzb added (one)":  addedText([]string{"Some & Release"}, 42, "Ann & Bob"),
		"nzb added (many)": addedText([]string{"First <Release>", "Second & Third"}, 42, ""),
		"invalid nzb ids":  fmt.Sprintf("<u><b>INVALID NZB IDS</b></u>\n\n<code>%s</code>", escape("bad<id>")),
		"results redacted": redactedMessage,
		"clear cancelled":  clearCancelledMessage,
		"authorized":       fmt.Sprintf("Authorized <code>%d</code>", -1001234567890),
		"unauthorized":     fmt.Sprintf("Unauthorized <code>%d</code>", -1001234567890),
		"plain reply":      "Provide a proper NZB ID or reply to a .NZB file",
	}
	for what, text := range messages {
		mustParseHTML(t, what, text)
	}
}

// TestStatusMessagesParse covers every branch of the status renderer, since
// each one builds its markup differently.
func TestStatusMessagesParse(t *testing.T) {
	base := nzbget.Group{
		NZBID:    7,
		NZBName:  "Release <with> & markup",
		FileSize: 1900637590,
		Health:   1000,
	}

	states := []nzbget.Group{
		func() nzbget.Group { g := base; g.Status = "FETCHING"; return g }(),
		func() nzbget.Group { g := base; g.Status = "DOWNLOADING"; g.DownloadedSize = 793260; return g }(),
		func() nzbget.Group { g := base; g.Status = "QUEUED"; return g }(),
		// Post-processing, both with a parseable progress line and without.
		func() nzbget.Group {
			g := base
			g.Status = "EXECUTING_SCRIPT"
			g.PostInfoText = "0 B / 48.3 MB, 0.0%, 0 B/s, ETA 23h23m23s"
			return g
		}(),
		func() nzbget.Group { g := base; g.Status = "EXECUTING_SCRIPT"; g.PostInfoText = "starting"; return g }(),
		func() nzbget.Group {
			g := base
			g.Status = "UNPACKING"
			g.PostStageProgress = 450
			g.PostStageTimeSec = 30
			return g
		}(),
		// A group NZBGet has not sized yet: the renderer must not divide by it.
		func() nzbget.Group { g := base; g.Status = "DOWNLOADING"; g.FileSize = 0; return g }(),
	}

	for _, group := range states {
		mustParseHTML(t, "status "+group.Status, renderStatus([]nzbget.Group{group}, 5146534))
	}

	// The "+N more task(s)" tail only appears past the display limit.
	var many []nzbget.Group
	for i := range statusTaskLimit + 3 {
		g := base
		g.NZBID = int64(i)
		g.Status = "DOWNLOADING"
		many = append(many, g)
	}
	rendered := renderStatus(many, 1000)
	if !strings.Contains(rendered, "+3 more task(s)") {
		t.Errorf("overflow tail missing from:\n%s", rendered)
	}
	mustParseHTML(t, "status overflow", rendered)
}

func TestSearchAndClearPagesParse(t *testing.T) {
	b := &Bot{cfg: &config.Config{SearchResultsPerPage: 2}}

	search := &searchSession{
		query: `tricky <query> & "quotes"`,
		results: []indexers.Result{
			{Title: "First <b>Release</b> & Co", GUID: "abc==", SizeText: "1.5 GB", Age: "2 Days"},
			{Title: "Second Release", GUID: "def==", SizeText: "700.0 MB", Age: "< 1 Day"},
			{Title: "Third Release", GUID: "ghi==", SizeText: "12.0 GB", Age: "41 Days"},
		},
	}
	for _, sortMode := range []string{"", "max", "min"} {
		search.sortMode = sortMode
		for page := range 2 {
			text, _, _ := b.searchPageText(search, page)
			mustParseHTML(t, fmt.Sprintf("search page %d sort %q", page, sortMode), text)
		}
	}

	clear := &clearSession{items: []string{"One & Two", "Three <four>", "Five"}}
	for page := range 2 {
		text, _, _ := b.clearPageText(clear, page)
		mustParseHTML(t, fmt.Sprintf("clear page %d", page), text)
	}
}

func TestPercent(t *testing.T) {
	cases := map[float64]string{
		2.1702838064197483: "2.2",
		37.40466253132422:  "37.4",
		24.00578817633272:  "24.0",
		0:                  "0.0",
		100:                "100.0",
		99.95:              "100.0",
	}
	for value, want := range cases {
		if got := percent(value); got != want {
			t.Errorf("percent(%v) = %q, want %q", value, got, want)
		}
	}
}

// TestServerStatsParse builds the /server message the way handleServer does,
// without needing a live host to read counters from.
func TestServerStatsParse(t *testing.T) {
	text := "<u><b>NZBGET STATS</b></u>\n" +
		"<code>Avg Speed:</code> <code>3.0 MB/s</code>\n" +
		"<code>NZBGet Uptime:</code> <code>8d6h36m13s</code>\n" +
		"<code>Disk:</code> <code>290.6 GB</code> <code>|</code> <code>Free:</code> <code>253.5 GB</code>\n\n" +
		"<u><b>SYSTEM STATS</b></u>\n" +
		"<code>Bot Uptime:</code> <code>1m4s</code>\n" +
		"<code>Disk:</code> <code>500.0 GB</code> <code>|</code> <code>Free:</code> <code>200.0 GB</code>\n\n" +
		"<code>CPU:</code> <code>12.5%</code> <code>|</code> <code>RAM:</code> <code>44.1%</code> <code>|</code> <code>DISK:</code> <code>60%</code>"
	mustParseHTML(t, "server stats", text)
}

// TestEscapedContentParse feeds markup-shaped text through the fields that
// carry user or indexer content, which must arrive escaped rather than parsed.
func TestEscapedContentParse(t *testing.T) {
	hostile := `</code><script>alert("x")</script><b>& <u>`

	group := nzbget.Group{NZBID: 1, Status: "DOWNLOADING", NZBName: hostile, FileSize: 100, DownloadedSize: 50}
	mustParseHTML(t, "hostile nzb name", renderStatus([]nzbget.Group{group}, 10))

	mustParseHTML(t, "hostile mention", mention(7, hostile))
	mustParseHTML(t, "hostile added list", addedText([]string{hostile}, 7, hostile))

	b := &Bot{cfg: &config.Config{SearchResultsPerPage: 5}}
	text, _, _ := b.searchPageText(&searchSession{
		query:   hostile,
		results: []indexers.Result{{Title: hostile, GUID: "x==", SizeText: "1.0 GB", Age: "1 Days"}},
	}, 0)
	mustParseHTML(t, "hostile search result", text)

	text, _, _ = b.clearPageText(&clearSession{items: []string{hostile}}, 0)
	mustParseHTML(t, "hostile clear item", text)
}
