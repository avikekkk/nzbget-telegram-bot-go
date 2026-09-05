package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/telegram/message/markup"
	"github.com/gotd/td/tg"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/indexers"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/telegraph"
)

const (
	maxSortFlag     = "--mx"
	minSortFlag     = "--mn"
	maxSessions     = 100
	telegraphTitle  = "NZB Search Results"
	telegraphAuthor = "USENET"
)

// redactedMessage replaces a result list once it is hidden, manually or on the
// redaction timer. It is a notice about the message, not content from it, so
// it is plain text; only indexer-supplied names stay monospace.
const redactedMessage = "RESULTS REDACTED"

// searchSession is one rendered set of results, kept so its pagination buttons
// keep working after the message is sent.
type searchSession struct {
	results []indexers.Result
	// found is the hit count before the cap, so a trimmed list can say so.
	found    int
	query    string
	sortMode string
	userID   int64

	// The Telegraph page is published after the results are already on screen,
	// so these are written by that goroutine while the pagination callbacks
	// read them. mu covers everything below it.
	mu            sync.Mutex
	page          int
	telegraph     *telegraph.Client
	telegraphURL  string
	telegraphPath string
	redacted      bool
	// target and msgID locate the results message once it has been sent, so
	// a session dropped for any reason can still be blanked on screen.
	target tg.InputPeerClass
	msgID  int

	// cancelRedact stops the auto-redact timer. It is guarded by Bot.mu, not
	// by mu above, because it is set and cleared alongside the session map.
	cancelRedact context.CancelFunc
}

// setMessage records where the results were posted.
func (s *searchSession) setMessage(target tg.InputPeerClass, msgID int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = target
	s.msgID = msgID
}

// message returns where the results were posted, if they were.
func (s *searchSession) message() (tg.InputPeerClass, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.target, s.msgID
}

// setTelegraph records a freshly published page and reports whether it is
// still wanted. A session redacted while the page was being built keeps its
// URL off the keyboard, and the caller blanks the page instead.
func (s *searchSession) setTelegraph(client *telegraph.Client, url, path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.redacted {
		return false
	}
	s.telegraph, s.telegraphURL, s.telegraphPath = client, url, path
	return true
}

// telegraphURLValue is the published page, or "" while it is still being made.
func (s *searchSession) telegraphURLValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.telegraphURL
}

// markRedacted closes the session to further publishing and hands back the
// page to blank, if one exists yet.
func (s *searchSession) markRedacted() (*telegraph.Client, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redacted = true
	return s.telegraph, s.telegraphPath
}

// setPage and currentPage track which page is on screen, so the edit that adds
// the Telegraph button does not drag a user who has already paged on back to
// the first page.
func (s *searchSession) setPage(page int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.page = page
}

func (s *searchSession) currentPage() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.page
}

// handleSearch queries the indexers and posts the first page of results.
func (b *Bot) handleSearch(c *Ctx) error {
	if c.args == "" {
		_, err := c.Reply("Provide a query")
		return err
	}

	query, sortMode, parseErr := parseSearchArgs(c.args)
	if parseErr != "" {
		_, err := c.Reply(parseErr)
		return err
	}
	if strings.TrimSpace(query) == "" {
		_, err := c.Reply("Provide a query")
		return err
	}

	sortText := ""
	switch sortMode {
	case "max":
		sortText = " (sorted by size: largest first)"
	case "min":
		sortText = " (sorted by size: smallest first)"
	}

	notice, err := c.Reply("Searching NZBs" + sortText)
	if err != nil {
		return err
	}

	start := time.Now()
	results, found, err := b.idx.Search(c.ctx, query, sortMode)
	b.log.Info("Search complete", "query", query, "sort", sortMode,
		"results", len(results), "found", found, "took", time.Since(start).Round(time.Millisecond))
	if err != nil {
		b.log.Error("Error occurred while searching", "err", err)
		if errors.Is(err, indexers.ErrNoIndexers) {
			return c.Edit(notice.ID, "No indexer configured, set NZBHYDRA_API_URL")
		}
		return c.Edit(notice.ID, b.failureText("Error occurred while searching"))
	}
	if len(results) == 0 {
		return c.Edit(notice.ID, "No results")
	}

	session := &searchSession{
		results:  results,
		found:    found,
		query:    query,
		sortMode: sortMode,
		userID:   c.userID,
	}
	token, err := newToken(8)
	if err != nil {
		return err
	}
	b.storeSearch(token, session)

	text, totalPages, page := b.searchPageText(session, 0)
	keyboard := b.searchMarkup(token, session, page, totalPages)

	if err := c.Delete(notice.ID); err != nil {
		b.log.Debug("Could not delete search notice", "err", err)
	}
	sent, err := c.ReplyMarkup(text, keyboard)
	if err != nil {
		return err
	}

	target, err := c.Peer()
	if err != nil {
		return err
	}
	session.setMessage(target, sent.ID)
	b.scheduleAutoRedact(token, target, sent.ID)

	// The Telegraph mirror is published only now. Building it first meant every
	// search waited on two more round trips before a single result appeared,
	// for a button most searches never use. The keyboard is edited once the
	// page exists, on whichever page the reader has since turned to.
	go func() {
		defer b.recoverPanic("telegraph publish")

		if !b.publishTelegraph(b.runCtx, session, results) {
			return
		}
		if b.getSearch(token) == nil {
			return
		}
		page := session.currentPage()
		text, totalPages, safePage := b.searchPageText(session, page)
		if err := b.editMarkup(b.runCtx, target, sent.ID, text, b.searchMarkup(token, session, safePage, totalPages)); err != nil {
			b.log.Debug("Could not attach the Telegraph button", "err", err)
		}
	}()
	return nil
}

// publishTelegraph mirrors the full result list to a Telegraph page and
// reports whether the page is live and wanted. A failure only costs the URL
// button, so it is logged rather than surfaced.
func (b *Bot) publishTelegraph(ctx context.Context, session *searchSession, results []indexers.Result) bool {
	client, err := b.telegraphAccount(ctx)
	if err != nil {
		b.log.Error("Telegraph failed", "err", err)
		return false
	}

	content := make([]telegraph.Node, 0, len(results)*2)
	for _, result := range results {
		content = append(content,
			telegraph.Tag("p",
				telegraph.Tag("code", telegraph.Text(result.Title)),
				telegraph.Text(fmt.Sprintf(" - %s  (Age: %s)", result.SizeText, result.Age)),
				telegraph.Tag("br"),
				telegraph.Text("NZB ID: "),
				telegraph.Tag("code", telegraph.Text(result.GUID)),
			),
		)
	}

	page, err := client.CreatePage(ctx, telegraphTitle, telegraphAuthor, content)
	if err != nil {
		b.log.Error("Telegraph failed", "err", err)
		return false
	}

	if !session.setTelegraph(client, page.URL, page.Path) {
		// Closed or auto-redacted while the page was being built. It lists every
		// result and its NZB ID, so it cannot be left standing.
		b.blankPage(ctx, client, page.Path)
		return false
	}
	return true
}

// telegraphAccount returns the account every search publishes under, creating
// it on first use. Making one per search cost a round trip before any result
// could be shown, and left a new throwaway account behind each time.
func (b *Bot) telegraphAccount(ctx context.Context) (*telegraph.Client, error) {
	b.telegraphMu.Lock()
	defer b.telegraphMu.Unlock()

	if b.telegraph != nil {
		return b.telegraph, nil
	}
	client, err := telegraph.NewAccount(ctx, "NZBSearch")
	if err != nil {
		return nil, err
	}
	b.telegraph = client
	return client, nil
}

// redactTelegraph blanks the page that accompanies a search. It lists every
// result and its NZB ID, so wiping only the Telegram message would leave the
// whole thing public.
func (b *Bot) redactTelegraph(ctx context.Context, session *searchSession) {
	client, path := session.markRedacted()
	b.blankPage(ctx, client, path)
}

// blankPage replaces a published page with a placeholder. The page is never
// deleted, because Telegraph has no delete: blanking it is the only redaction
// there is.
func (b *Bot) blankPage(ctx context.Context, client *telegraph.Client, path string) {
	if client == nil || path == "" {
		return
	}
	content := []telegraph.Node{telegraph.Tag("p", telegraph.Text("REDACTED"))}
	if err := client.EditPage(ctx, path, telegraphTitle, telegraphAuthor, content); err != nil {
		b.log.Error("Telegraph redact failed", "err", err)
	}
}

// scheduleAutoRedact wipes the results after AUTOREDACT seconds, if set.
func (b *Bot) scheduleAutoRedact(token string, target tg.InputPeerClass, msgID int) {
	if b.cfg.AutoRedact <= 0 {
		return
	}

	ctx, cancel := context.WithCancel(b.runCtx)

	b.mu.Lock()
	session, ok := b.searches[token]
	if ok {
		if session.cancelRedact != nil {
			session.cancelRedact()
		}
		session.cancelRedact = cancel
	}
	b.mu.Unlock()

	if !ok {
		cancel()
		return
	}

	go func() {
		defer cancel()
		defer b.recoverPanic("auto-redact")

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(b.cfg.AutoRedact) * time.Second):
		}

		expired := b.takeSearch(token)
		if expired == nil {
			return
		}
		b.redactSession(b.runCtx, expired)
	}()
}

// redactSession blanks everything a dropped session left in public: the
// Telegraph page and the results message itself.
func (b *Bot) redactSession(ctx context.Context, session *searchSession) {
	b.redactTelegraph(ctx, session)
	target, msgID := session.message()
	if target == nil {
		return
	}
	if err := b.edit(ctx, target, msgID, redactedMessage); err != nil {
		b.log.Debug("Could not mark results redacted", "err", err)
	}
}

func (b *Bot) storeSearch(token string, session *searchSession) {
	b.mu.Lock()
	b.searches[token] = session
	b.searchOrder = append(b.searchOrder, token)

	// Sessions pushed out by the cap are redacted like any other: their
	// message and page list every result and NZB ID, and with the timer
	// cancelled nothing else would ever hide them.
	var evicted []*searchSession
	for len(b.searchOrder) > maxSessions {
		oldest := b.searchOrder[0]
		b.searchOrder = b.searchOrder[1:]
		if old, ok := b.searches[oldest]; ok {
			if old.cancelRedact != nil {
				old.cancelRedact()
				old.cancelRedact = nil
			}
			delete(b.searches, oldest)
			evicted = append(evicted, old)
		}
	}
	b.mu.Unlock()

	for _, old := range evicted {
		old := old
		go func() {
			defer b.recoverPanic("evict search")
			b.redactSession(b.runCtx, old)
		}()
	}
}

func (b *Bot) getSearch(token string) *searchSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.searches[token]
}

// takeSearch removes and returns a session, so a close and an auto-redact
// firing together cannot both act on it. The auto-redact timer is stopped on
// the way out, under the same lock that set it.
func (b *Bot) takeSearch(token string) *searchSession {
	b.mu.Lock()
	defer b.mu.Unlock()

	session, ok := b.searches[token]
	if !ok {
		return nil
	}
	if session.cancelRedact != nil {
		session.cancelRedact()
		session.cancelRedact = nil
	}
	delete(b.searches, token)
	for i, candidate := range b.searchOrder {
		if candidate == token {
			b.searchOrder = append(b.searchOrder[:i], b.searchOrder[i+1:]...)
			break
		}
	}
	return session
}

func (b *Bot) searchPageText(session *searchSession, page int) (string, int, int) {
	perPage := b.cfg.SearchResultsPerPage
	total := len(session.results)
	totalPages := max(1, (total+perPage-1)/perPage)
	page = min(max(page, 0), totalPages-1)

	start := page * perPage
	end := min(start+perPage, total)

	entries := make([]string, 0, end-start)
	for _, result := range session.results[start:end] {
		entries = append(entries, fmt.Sprintf(
			"<code>%s</code> - %s  (Age: %s)\nNZB ID: <code>%s</code>",
			escape(result.Title), result.SizeText, result.Age, result.GUID))
	}

	sortInfo := ""
	switch session.sortMode {
	case "max":
		sortInfo = " (sorted by size: max to min)"
	case "min":
		sortInfo = " (sorted by size: min to max)"
	}

	// Say when the list is only part of what the indexers returned, rather
	// than presenting a capped set as if it were everything.
	shown := fmt.Sprintf("Total: %d", total)
	if session.found > total {
		shown = fmt.Sprintf("Showing %d of %d", total, session.found)
	}

	header := fmt.Sprintf("<u><b>NZB SEARCH RESULTS%s</b></u>", sortInfo)
	meta := fmt.Sprintf("<b>Query:</b> <code>%s</code>\n<b>Page %d/%d | %s</b>",
		escape(session.query), page+1, totalPages, shown)
	return header + "\n" + meta + "\n\n" + strings.Join(entries, "\n\n"), totalPages, page
}

func (b *Bot) searchMarkup(token string, session *searchSession, page, totalPages int) tg.ReplyMarkupClass {
	var nav []tg.KeyboardButtonClass
	if page > 0 {
		nav = append(nav, markup.Callback("PREV", []byte(fmt.Sprintf("srch:%s:%d", token, page-1))))
	}
	nav = append(nav, markup.Callback(fmt.Sprintf("%d/%d", page+1, totalPages), []byte("srchnoop")))
	if page < totalPages-1 {
		nav = append(nav, markup.Callback("NEXT", []byte(fmt.Sprintf("srch:%s:%d", token, page+1))))
	}

	rows := []tg.KeyboardButtonRow{markup.Row(nav...)}
	if url := session.telegraphURLValue(); url != "" {
		rows = append(rows, markup.Row(markup.URL("URL", url)))
	}
	rows = append(rows, markup.Row(markup.Callback("CLOSE", []byte("srchc:"+token))))
	return markup.InlineKeyboard(rows...)
}

// onSearchCallback drives the pagination and close buttons.
func (b *Bot) onSearchCallback(cb *callback) error {
	data := cb.data

	if data == "srchnoop" {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	if token, ok := strings.CutPrefix(data, "srchc:"); ok {
		session := b.getSearch(token)
		if session == nil {
			return b.answer(cb.ctx, cb.queryID, "Session expired", true)
		}
		if cb.userID != session.userID {
			return b.answer(cb.ctx, cb.queryID, "Only requester can close this.", true)
		}
		if closed := b.takeSearch(token); closed != nil {
			b.redactTelegraph(cb.ctx, closed)
		}
		if err := b.edit(cb.ctx, cb.peer, cb.msgID, redactedMessage); err != nil {
			b.log.Debug("Could not mark results redacted", "err", err)
		}
		return b.answer(cb.ctx, cb.queryID, "Closed", false)
	}

	rest, ok := strings.CutPrefix(data, "srch:")
	if !ok {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}
	token, pageText, found := strings.Cut(rest, ":")
	if !found {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	session := b.getSearch(token)
	if session == nil {
		return b.answer(cb.ctx, cb.queryID, "Session expired", true)
	}
	if cb.userID != session.userID {
		return b.answer(cb.ctx, cb.queryID, "Only requester can change pages.", true)
	}
	page, err := strconv.Atoi(pageText)
	if err != nil {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	text, totalPages, safePage := b.searchPageText(session, page)
	session.setPage(safePage)
	keyboard := b.searchMarkup(token, session, safePage, totalPages)
	if err := b.editMarkup(cb.ctx, cb.peer, cb.msgID, text, keyboard); err != nil {
		b.log.Debug("Could not turn search page", "err", err)
	}
	return b.answer(cb.ctx, cb.queryID, "", false)
}

// parseSearchArgs splits the query from an optional sort flag. A flag counts
// only as the last token, so a query may still contain the characters.
func parseSearchArgs(text string) (query, sortMode, parseError string) {
	args := searchTerms(text)
	if len(args) == 0 {
		return text, "", ""
	}

	maxCount, minCount := 0, 0
	for _, arg := range args {
		switch arg {
		case maxSortFlag:
			maxCount++
		case minSortFlag:
			minCount++
		}
	}
	if maxCount > 1 || minCount > 1 {
		return "", "", "Use --mx/--mn only once, at the end."
	}
	if maxCount > 0 && minCount > 0 {
		return "", "", "Use either --mx or --mn, not both."
	}

	if maxCount > 0 || minCount > 0 {
		last := args[len(args)-1]
		if last != maxSortFlag && last != minSortFlag {
			return "", "", "Use --mx or --mn only at the end. Example: /search ubuntu --mn"
		}
		if last == maxSortFlag {
			sortMode = "max"
		} else {
			sortMode = "min"
		}
		args = args[:len(args)-1]
	}
	return strings.Join(args, " "), sortMode, ""
}

// searchTerms splits a query into words. A search query is prose, not a shell
// command, so nothing here can fail and no character is special: an apostrophe
// in "Marvel's Spider-Man 2" used to open a quoted run that never closed,
// which swallowed the rest of the line -- including a trailing --mx, so the
// sort was silently dropped. Surrounding quotes are still trimmed, for anyone
// who quotes a phrase out of habit, but an internal apostrophe is kept: the
// indexers match titles literally, and "Marvels" does not find "Marvel's".
func searchTerms(text string) []string {
	var terms []string
	for _, field := range strings.Fields(text) {
		if field = strings.Trim(field, `'"`); field != "" {
			terms = append(terms, field)
		}
	}
	return terms
}

func newToken(length int) (string, error) {
	raw := make([]byte, (length+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw)[:length], nil
}
