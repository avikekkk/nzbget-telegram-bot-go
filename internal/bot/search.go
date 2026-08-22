package bot

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
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

// searchSession is one rendered set of results, kept so its pagination buttons
// keep working after the message is sent.
type searchSession struct {
	results []indexers.Result
	// found is the hit count before the cap, so a trimmed list can say so.
	found    int
	query    string
	sortMode string
	userID   int64

	telegraph     *telegraph.Client
	telegraphURL  string
	telegraphPath string

	cancelRedact context.CancelFunc
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

	results, found, err := b.idx.Search(c.ctx, query, sortMode)
	if err != nil {
		b.log.Error("Error occurred while searching", "err", err)
		return c.Edit(notice.ID, "Error occurred while searching")
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
	b.publishTelegraph(c.ctx, session, results)

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
	b.scheduleAutoRedact(token, target, sent.ID)
	return nil
}

// publishTelegraph mirrors the full result list to a Telegraph page. A failure
// only costs the URL button, so it is logged rather than surfaced.
func (b *Bot) publishTelegraph(ctx context.Context, session *searchSession, results []indexers.Result) {
	client, err := telegraph.NewAccount(ctx, "NZBSearch")
	if err != nil {
		b.log.Error("Telegraph failed", "err", err)
		return
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
		return
	}

	// The access token stays with the session: revoking it after creation
	// leaves the page permanently uneditable, and so impossible to redact.
	session.telegraph = client
	session.telegraphURL = page.URL
	session.telegraphPath = page.Path
}

// redactTelegraph blanks the page that accompanies a search. It lists every
// result and its NZB ID, so wiping only the Telegram message would leave the
// whole thing public.
func (b *Bot) redactTelegraph(ctx context.Context, session *searchSession) {
	if session.telegraph == nil || session.telegraphPath == "" {
		return
	}
	content := []telegraph.Node{telegraph.Tag("p", telegraph.Text("REDACTED"))}
	if err := session.telegraph.EditPage(ctx, session.telegraphPath, telegraphTitle, telegraphAuthor, content); err != nil {
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
		b.redactTelegraph(b.runCtx, expired)
		if err := b.edit(b.runCtx, target, msgID, "<code>RESULTS REDACTED</code>"); err != nil {
			b.log.Debug("Could not mark results redacted", "err", err)
		}
	}()
}

func (b *Bot) storeSearch(token string, session *searchSession) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.searches[token] = session
	b.searchOrder = append(b.searchOrder, token)
	for len(b.searchOrder) > maxSessions {
		oldest := b.searchOrder[0]
		b.searchOrder = b.searchOrder[1:]
		if evicted, ok := b.searches[oldest]; ok {
			if evicted.cancelRedact != nil {
				evicted.cancelRedact()
			}
			delete(b.searches, oldest)
		}
	}
}

func (b *Bot) getSearch(token string) *searchSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.searches[token]
}

// takeSearch removes and returns a session, so a close and an auto-redact
// firing together cannot both act on it.
func (b *Bot) takeSearch(token string) *searchSession {
	b.mu.Lock()
	defer b.mu.Unlock()

	session, ok := b.searches[token]
	if !ok {
		return nil
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
	if session.telegraphURL != "" {
		rows = append(rows, markup.Row(markup.URL("URL", session.telegraphURL)))
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
			if closed.cancelRedact != nil {
				closed.cancelRedact()
			}
			b.redactTelegraph(cb.ctx, closed)
		}
		if err := b.edit(cb.ctx, cb.peer, cb.msgID, "<code>RESULTS REDACTED</code>"); err != nil {
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
	keyboard := b.searchMarkup(token, session, safePage, totalPages)
	if err := b.editMarkup(cb.ctx, cb.peer, cb.msgID, text, keyboard); err != nil {
		b.log.Debug("Could not turn search page", "err", err)
	}
	return b.answer(cb.ctx, cb.queryID, "", false)
}

// parseSearchArgs splits the query from an optional sort flag. A flag counts
// only as the last token, so a query may still contain the characters.
func parseSearchArgs(text string) (query, sortMode, parseError string) {
	args := splitArgs(text)
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

// splitArgs splits on whitespace while honouring quoted runs, the part of
// shell-style parsing that matters for a search query.
func splitArgs(text string) []string {
	var (
		args    []string
		current strings.Builder
		quote   rune
		started bool
	)
	flush := func() {
		if started {
			args = append(args, current.String())
			current.Reset()
			started = false
		}
	}

	for _, r := range text {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			started = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	flush()
	return args
}

func newToken(length int) (string, error) {
	raw := make([]byte, (length+1)/2)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw)[:length], nil
}
