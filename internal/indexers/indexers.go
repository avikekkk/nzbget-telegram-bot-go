// Package indexers searches the configured NZB indexers.
package indexers

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/config"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/crypt"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/util"
)

const userAgent = "Mozilla/5.0 (Linux; Android 11; M2101K7BI) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/104.0.0.0 Mobile Safari/537.36"

var tagPattern = regexp.MustCompile(`<.*?>`)

// Indexer is one configured search source.
type Indexer struct {
	Key      string
	API      string
	APIURL   string
	URL      string
	Internal bool
	DelLimit bool
}

// Result is one search hit, ready to render.
type Result struct {
	Title     string
	GUID      string
	SizeBytes int64
	SizeText  string
	Age       string
}

// Set is the configured collection of indexers.
type Set struct {
	byKey map[string]Indexer
	http  *http.Client
}

// New builds the indexer set from configuration.
func New(cfg *config.Config) *Set {
	jar, _ := cookiejar.New(nil) // New never fails with nil options.
	return &Set{
		byKey: map[string]Indexer{
			"nzbhydra": {
				Key:      "nzbhydra",
				API:      cfg.NZBHydraAPI,
				APIURL:   cfg.NZBHydraAPIURL,
				URL:      cfg.NZBHydraURL,
				Internal: true,
				DelLimit: true,
			},
		},
		http: &http.Client{Timeout: 60 * time.Second, Jar: jar},
	}
}

// Get returns the indexer registered under key.
func (s *Set) Get(key string) (Indexer, bool) {
	indexer, ok := s.byKey[key]
	return indexer, ok
}

// DownloadURL builds the NZB download link for a result GUID.
func (i Indexer) DownloadURL(guid string) string {
	replacer := strings.NewReplacer("{guid}", guid, "{api_key}", i.API)
	return replacer.Replace(i.URL)
}

// MaxResults caps how many hits a search keeps, to bound the message and the
// Telegraph page. The cap is applied after sorting, never before -- trimming
// first would sort a slice of whatever the indexer happened to return first
// (usually the newest), so "largest" would mean "largest among the newest 100"
// and the biggest releases would never appear at all.
const MaxResults = 100

// Search queries every usable indexer and returns the merged results, capped
// at MaxResults. The second return is how many hits there were before the cap.
// sortMode is "max" (largest first), "min" (smallest first) or "" (as found).
func (s *Set) Search(ctx context.Context, query, sortMode string) ([]Result, int, error) {
	var all []Result
	searched := 0
	for _, indexer := range s.byKey {
		if !indexer.usable() {
			continue
		}
		searched++
		var (
			results []Result
			err     error
		)
		if indexer.Internal {
			results, err = s.searchInternal(ctx, query, indexer)
		} else {
			results, err = s.searchNewznab(ctx, query, indexer)
		}
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", indexer.Key, err)
		}
		all = append(all, results...)
	}
	if searched == 0 {
		return nil, 0, ErrNoIndexers
	}

	switch sortMode {
	case "max":
		sort.SliceStable(all, func(a, b int) bool { return all[a].SizeBytes > all[b].SizeBytes })
	case "min":
		sort.SliceStable(all, func(a, b int) bool { return all[a].SizeBytes < all[b].SizeBytes })
	}

	found := len(all)
	if len(all) > MaxResults {
		all = all[:MaxResults]
	}
	return all, found, nil
}

// ErrNoIndexers means nothing is configured to search: an internal indexer
// with no API URL, or a newznab one with no key, is skipped rather than asked.
var ErrNoIndexers = errors.New("no indexer configured")

// usable reports whether the indexer has enough configuration to be queried.
func (i Indexer) usable() bool {
	if i.Internal {
		return i.APIURL != ""
	}
	return i.API != ""
}

// trimAPI strips a trailing "/api" path segment.
//
// A suffix trim, not a character-set trim: cutting the set "/api" from the end
// over-trims a URL whose path merely ends in those characters, e.g.
// ".../hydra/api" losing more than the segment.
func trimAPI(apiURL string) string {
	return strings.TrimSuffix(apiURL, "/api")
}

type internalSearchResponse struct {
	SearchResults []struct {
		SearchResultID any    `json:"searchResultId"`
		Title          string `json:"title"`
		Size           int64  `json:"size"`
		Epoch          int64  `json:"epoch"`
	} `json:"searchResults"`
}

func (s *Set) searchInternal(ctx context.Context, query string, indexer Indexer) ([]Result, error) {
	body, err := json.Marshal(map[string]string{
		"query":    query,
		"category": "All",
		"mode":     "search",
	})
	if err != nil {
		return nil, err
	}

	var decoded internalSearchResponse
	if err := s.doJSON(ctx, http.MethodPost, trimAPI(indexer.APIURL)+"/internalapi/search", body, &decoded); err != nil {
		return nil, err
	}

	// Every hit is returned; Search does the capping, once the whole set from
	// every indexer has been sorted together.
	results := make([]Result, 0, len(decoded.SearchResults))
	for _, item := range decoded.SearchResults {
		guid, err := crypt.Encrypt(fmt.Sprintf("%s|%s", anyToString(item.SearchResultID), indexer.Key))
		if err != nil {
			return nil, err
		}
		results = append(results, Result{
			Title:     tagPattern.ReplaceAllString(item.Title, ""),
			GUID:      guid,
			SizeBytes: item.Size,
			SizeText:  util.ConvertSize(item.Size),
			Age:       util.AgeFromEpoch(item.Epoch),
		})
	}
	return results, nil
}

// newznabFeed matches the parts of a newznab RSS response the bot uses.
// The attribute elements are namespaced (newznab:attr); encoding/xml matches
// on the local name when no namespace is given, so "attr" is enough.
type newznabFeed struct {
	Items []struct {
		Title     string `xml:"title"`
		Enclosure struct {
			Length string `xml:"length,attr"`
		} `xml:"enclosure"`
		Attrs []struct {
			Name  string `xml:"name,attr"`
			Value string `xml:"value,attr"`
		} `xml:"attr"`
	} `xml:"channel>item"`
}

func (s *Set) searchNewznab(ctx context.Context, query string, indexer Indexer) ([]Result, error) {
	params := url.Values{}
	params.Set("apikey", indexer.API)
	params.Set("t", "search")
	params.Set("q", query)
	params.Set("pw", "1")
	params.Set("extended", "1")
	params.Set("offset", "0")
	if !indexer.DelLimit {
		params.Set("limit", "100")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexer.APIURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A failing indexer is skipped rather than failing the whole search.
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return nil, nil
	}

	var feed newznabFeed
	if err := xml.NewDecoder(resp.Body).Decode(&feed); err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(feed.Items))
	for _, item := range feed.Items {
		var rawGUID, usenetDate string
		for _, attr := range item.Attrs {
			switch attr.Name {
			case "guid":
				rawGUID = attr.Value
			case "usenetdate":
				usenetDate = attr.Value
			}
		}
		if rawGUID == "" {
			continue
		}
		guid, err := crypt.Encrypt(fmt.Sprintf("%s|%s", rawGUID, indexer.Key))
		if err != nil {
			return nil, err
		}
		size, _ := strconv.ParseInt(item.Enclosure.Length, 10, 64)
		results = append(results, Result{
			Title:     tagPattern.ReplaceAllString(item.Title, ""),
			GUID:      guid,
			SizeBytes: size,
			SizeText:  util.ConvertSize(size),
			Age:       util.AgeFromRFC(usenetDate),
		})
	}
	return results, nil
}

type addNZBsResponse struct {
	Successful bool `json:"successful"`
}

// AddInternal hands a search result to NZBHydra's own downloader integration
// and reports whether it was accepted.
func (s *Set) AddInternal(ctx context.Context, resultID, apiURL, downloaderName string) (bool, error) {
	body, err := json.Marshal(map[string]any{
		"downloaderName": downloaderName,
		"searchResults":  []map[string]string{{"searchResultId": resultID}},
		"category":       "",
	})
	if err != nil {
		return false, err
	}

	var decoded addNZBsResponse
	if err := s.doJSON(ctx, http.MethodPut, trimAPI(apiURL)+"/internalapi/downloader/addNzbs", body, &decoded); err != nil {
		return false, err
	}
	return decoded.Successful, nil
}

func (s *Set) doJSON(ctx context.Context, method, endpoint string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	if token, err := s.xsrfToken(ctx, req.URL); err != nil {
		return fmt.Errorf("%s %s: fetching XSRF token: %w", method, req.URL.Redacted(), err)
	} else if token != "" {
		req.Header.Set("X-XSRF-TOKEN", token)
	}

	resp, err := s.http.Do(req)
	if err != nil {
		return redactURLError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%s %s returned %s", method, req.URL.Redacted(), resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// xsrfToken returns NZBHydra's HYDRA-XSRF-TOKEN cookie for target's host,
// visiting the site root first if the jar has none. NZBHydra 9+ requires it
// as the X-XSRF-TOKEN header on POST/PUT/DELETE to /internalapi. An empty
// token means the server doesn't use CSRF protection.
func (s *Set) xsrfToken(ctx context.Context, target *url.URL) (string, error) {
	const cookieName = "HYDRA-XSRF-TOKEN"
	if s.http.Jar == nil {
		return "", nil
	}
	find := func() string {
		for _, c := range s.http.Jar.Cookies(target) {
			if c.Name == cookieName {
				return c.Value
			}
		}
		return ""
	}
	if token := find(); token != "" {
		return token, nil
	}

	root := *target
	root.Path = "/"
	if i := strings.Index(target.Path, "/internalapi"); i >= 0 {
		root.Path = target.Path[:i] + "/"
	}
	root.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.http.Do(req)
	if err != nil {
		return "", redactURLError(err)
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return find(), nil
}

// redactURLError strips credentials from the URL inside a transport error.
func redactURLError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		if u, perr := url.Parse(urlErr.URL); perr == nil {
			urlErr.URL = u.Redacted()
		}
	}
	return err
}

// anyToString renders a JSON value that may arrive as a number or a string.
func anyToString(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case float64:
		return strconv.FormatInt(int64(value), 10)
	case nil:
		return ""
	default:
		return fmt.Sprint(value)
	}
}
