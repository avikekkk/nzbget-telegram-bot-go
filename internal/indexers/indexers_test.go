package indexers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/config"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/crypt"
)

const newznabFeedXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <item>
      <title>Ubuntu &lt;b&gt;24.04&lt;/b&gt; ISO</title>
      <enclosure url="http://example.invalid/1" length="5368709120" type="application/x-nzb"/>
      <newznab:attr name="guid" value="abc123"/>
      <newznab:attr name="usenetdate" value="Mon, 01 Jan 2024 12:00:00 +0000"/>
    </item>
    <item>
      <title>No GUID Here</title>
      <enclosure url="http://example.invalid/2" length="100"/>
    </item>
  </channel>
</rss>`

func TestSearchNewznab(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("q"); got != "ubuntu" {
			t.Errorf("query = %q, want ubuntu", got)
		}
		if got := r.URL.Query().Get("apikey"); got != "secret" {
			t.Errorf("apikey = %q", got)
		}
		w.Header().Set("Content-Type", "application/xml")
		io.WriteString(w, newznabFeedXML)
	}))
	defer server.Close()

	set := &Set{
		byKey: map[string]Indexer{"demo": {Key: "demo", API: "secret", APIURL: server.URL}},
		http:  server.Client(),
	}

	results, _, err := set.Search(context.Background(), "ubuntu", "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// The second item has no GUID, so there is nothing to grab it by.
	if len(results) != 1 {
		t.Fatalf("results = %+v, want 1", results)
	}

	got := results[0]
	if got.Title != "Ubuntu 24.04 ISO" {
		t.Errorf("Title = %q, want the inline markup stripped", got.Title)
	}
	if got.SizeBytes != 5368709120 || got.SizeText != "5.0 GB" {
		t.Errorf("size = %d (%q)", got.SizeBytes, got.SizeText)
	}

	// The displayed ID must decode back to the indexer's own GUID and key.
	plaintext, err := crypt.Decrypt(got.GUID)
	if err != nil || plaintext != "abc123|demo" {
		t.Errorf("decoded GUID = %q, %v", plaintext, err)
	}
}

func TestSearchSorts(t *testing.T) {
	set := &Set{byKey: map[string]Indexer{}, http: http.DefaultClient}
	set.byKey["demo"] = Indexer{Key: "demo"} // no API key: skipped

	results, _, err := set.Search(context.Background(), "x", "max")
	if err != nil || len(results) != 0 {
		t.Fatalf("Search over a disabled indexer = %+v, %v", results, err)
	}
}

// TestSearchSortsBeforeCapping is the regression test for a search that
// trimmed to MaxResults and only then sorted. An indexer returns hits newest
// first, so the extremes of size sit anywhere in the list -- often past the
// cap. Sorting a pre-trimmed slice made "--mx" mean "largest among the newest
// 100", which hid a 105 GB remux behind a page of 400 MB episodes.
func TestSearchSortsBeforeCapping(t *testing.T) {
	const total = MaxResults + 50

	// Sizes ascend through the feed, so both extremes are past the cap in one
	// direction or the other: the largest is last, and the smallest is first
	// only until the list is reversed by sorting.
	var items strings.Builder
	for i := range total {
		fmt.Fprintf(&items, `<item>
			<title>Release %03d</title>
			<enclosure length="%d"/>
			<newznab:attr name="guid" value="guid-%03d"/>
		</item>`, i, (i+1)*1_000_000, i)
	}
	feed := `<?xml version="1.0"?><rss xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
		<channel>` + items.String() + `</channel></rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, feed)
	}))
	defer server.Close()

	set := &Set{
		byKey: map[string]Indexer{"demo": {Key: "demo", API: "k", APIURL: server.URL, DelLimit: true}},
		http:  server.Client(),
	}

	smallest, largest := int64(1_000_000), int64(total*1_000_000)

	cases := []struct {
		sortMode string
		wantHead int64
	}{
		{"max", largest},
		{"min", smallest},
	}
	for _, c := range cases {
		results, found, err := set.Search(context.Background(), "q", c.sortMode)
		if err != nil {
			t.Fatalf("Search(%q): %v", c.sortMode, err)
		}
		if found != total {
			t.Errorf("Search(%q) found = %d, want %d before the cap", c.sortMode, found, total)
		}
		if len(results) != MaxResults {
			t.Errorf("Search(%q) returned %d results, want the cap of %d",
				c.sortMode, len(results), MaxResults)
		}
		if results[0].SizeBytes != c.wantHead {
			t.Errorf("Search(%q) starts at %d, want %d -- the extreme is past the cap, "+
				"so this fails if the trim happens before the sort",
				c.sortMode, results[0].SizeBytes, c.wantHead)
		}
	}

	// Without a sort flag the order is left as the indexer gave it, and the cap
	// simply takes the head of that.
	results, found, err := set.Search(context.Background(), "q", "")
	if err != nil {
		t.Fatalf("Search(unsorted): %v", err)
	}
	if found != total || len(results) != MaxResults || results[0].SizeBytes != smallest {
		t.Errorf("unsorted search = %d of %d starting at %d",
			len(results), found, results[0].SizeBytes)
	}
}

func TestAddInternalPostsSearchResult(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/internalapi/downloader/addNzbs" {
			t.Errorf("path = %q", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&payload)
		io.WriteString(w, `{"successful": true}`)
	}))
	defer server.Close()

	set := &Set{byKey: map[string]Indexer{}, http: server.Client()}
	// A trailing "/api" is a path segment to drop, not a character set to trim.
	ok, err := set.AddInternal(context.Background(), "res-1", server.URL+"/api", "NZBGet")
	if err != nil || !ok {
		t.Fatalf("AddInternal = %v, %v", ok, err)
	}
	if payload["downloaderName"] != "NZBGet" {
		t.Errorf("payload = %+v", payload)
	}
}

func TestDownloadURL(t *testing.T) {
	set := New(&config.Config{
		NZBHydraAPI: "key123",
		NZBHydraURL: "https://example.invalid/getnzb/api/{guid}?apikey={api_key}",
	})
	indexer, ok := set.Get("nzbhydra")
	if !ok {
		t.Fatal("nzbhydra indexer not registered")
	}
	want := "https://example.invalid/getnzb/api/abc?apikey=key123"
	if got := indexer.DownloadURL("abc"); got != want {
		t.Errorf("DownloadURL = %q, want %q", got, want)
	}
}
