package bot

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gotd/td/telegram/message/markup"
	"github.com/gotd/td/tg"
)

// clearSession is a pending /clear waiting on its confirmation button.
type clearSession struct {
	userID     int64
	queueIDs   []int64
	historyIDs []int64
	items      []string
}

// handleClear lists everything /clear would wipe and asks for confirmation.
func (b *Bot) handleClear(c *Ctx) error {
	queue, err := b.nzb.ListGroups(c.ctx)
	if err != nil {
		b.log.Error("clear: RPC failed", "err", err)
		_, replyErr := c.Reply("Failed to query NZBGet")
		return replyErr
	}
	history, err := b.nzb.History(c.ctx, false)
	if err != nil {
		b.log.Error("clear: RPC failed", "err", err)
		_, replyErr := c.Reply("Failed to query NZBGet")
		return replyErr
	}

	session := &clearSession{userID: c.userID}
	for _, group := range queue {
		if group.NZBID == 0 {
			continue
		}
		session.queueIDs = append(session.queueIDs, group.NZBID)
		session.items = append(session.items, firstNonEmpty(
			group.NZBName, group.NZBNicename, fmt.Sprintf("NZBID %d", group.NZBID)))
	}
	for _, item := range history {
		if item.NZBID == 0 {
			continue
		}
		session.historyIDs = append(session.historyIDs, item.NZBID)
		session.items = append(session.items, firstNonEmpty(
			item.Name, item.NZBName, fmt.Sprintf("NZBID %d", item.NZBID)))
	}

	if len(session.queueIDs) == 0 && len(session.historyIDs) == 0 {
		_, err := c.Reply("History empty")
		return err
	}

	token, err := newToken(10)
	if err != nil {
		return err
	}
	b.storeClear(token, session)

	text, totalPages, page := b.clearPageText(session, 0)
	_, err = c.ReplyMarkup(text, b.clearMarkup(token, page, totalPages))
	return err
}

func (b *Bot) storeClear(token string, session *clearSession) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.clears[token] = session
	b.clearOrder = append(b.clearOrder, token)
	for len(b.clearOrder) > maxSessions {
		oldest := b.clearOrder[0]
		b.clearOrder = b.clearOrder[1:]
		delete(b.clears, oldest)
	}
}

func (b *Bot) getClear(token string) *clearSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clears[token]
}

func (b *Bot) takeClear(token string) *clearSession {
	b.mu.Lock()
	defer b.mu.Unlock()

	session, ok := b.clears[token]
	if !ok {
		return nil
	}
	delete(b.clears, token)
	for i, candidate := range b.clearOrder {
		if candidate == token {
			b.clearOrder = append(b.clearOrder[:i], b.clearOrder[i+1:]...)
			break
		}
	}
	return session
}

func (b *Bot) clearPageText(session *clearSession, page int) (string, int, int) {
	perPage := b.cfg.SearchResultsPerPage
	total := len(session.items)
	totalPages := max(1, (total+perPage-1)/perPage)
	page = min(max(page, 0), totalPages-1)

	start := page * perPage
	end := min(start+perPage, total)

	// Numbered from the start of the whole list, not the page, so the count
	// keeps running across pages instead of restarting at 1 on each.
	entries := make([]string, 0, end-start)
	for offset, name := range session.items[start:end] {
		entries = append(entries, fmt.Sprintf("<code>%d. %s</code>", start+offset+1, escape(name)))
	}

	header := fmt.Sprintf("<u><b>NZBGET HISTORY</b></u>\n<b>Page %d/%d | Total: %d</b>",
		page+1, totalPages, total)
	return header + "\n\n" + strings.Join(entries, "\n\n"), totalPages, page
}

func (b *Bot) clearMarkup(token string, page, totalPages int) tg.ReplyMarkupClass {
	var nav []tg.KeyboardButtonClass
	if page > 0 {
		nav = append(nav, markup.Callback("PREV", []byte(fmt.Sprintf("clrpg:%s:%d", token, page-1))))
	}
	nav = append(nav, markup.Callback(fmt.Sprintf("%d/%d", page+1, totalPages), []byte("clrnoop")))
	if page < totalPages-1 {
		nav = append(nav, markup.Callback("NEXT", []byte(fmt.Sprintf("clrpg:%s:%d", token, page+1))))
	}

	return markup.InlineKeyboard(
		markup.Row(nav...),
		markup.Row(
			markup.Callback("CONFIRM", []byte("clear:confirm:"+token)),
			markup.Callback("CANCEL", []byte("clear:cancel:"+token)),
		),
	)
}

// onClearCallback drives the pagination, confirm and cancel buttons.
func (b *Bot) onClearCallback(cb *callback) error {
	data := cb.data

	if data == "clrnoop" {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	if rest, ok := strings.CutPrefix(data, "clrpg:"); ok {
		token, pageText, found := strings.Cut(rest, ":")
		if !found {
			return b.answer(cb.ctx, cb.queryID, "", false)
		}
		session := b.getClear(token)
		if session == nil {
			return b.answer(cb.ctx, cb.queryID, "Clear request expired.", true)
		}
		if cb.userID != session.userID {
			return b.answer(cb.ctx, cb.queryID, "Only requester can change pages.", true)
		}
		page, err := strconv.Atoi(pageText)
		if err != nil {
			return b.answer(cb.ctx, cb.queryID, "", false)
		}
		text, totalPages, safePage := b.clearPageText(session, page)
		if err := b.editMarkup(cb.ctx, cb.peer, cb.msgID, text, b.clearMarkup(token, safePage, totalPages)); err != nil {
			b.log.Debug("Could not turn clear page", "err", err)
		}
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	rest, ok := strings.CutPrefix(data, "clear:")
	if !ok {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}
	action, token, found := strings.Cut(rest, ":")
	if !found {
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	session := b.getClear(token)
	if session == nil {
		return b.answer(cb.ctx, cb.queryID, "Clear request expired.", true)
	}
	if cb.userID != session.userID {
		return b.answer(cb.ctx, cb.queryID, "Only requester can use this button.", true)
	}

	switch action {
	case "cancel":
		b.takeClear(token)
		if err := b.edit(cb.ctx, cb.peer, cb.msgID, "<code>CLEAR CANCELLED</code>"); err != nil {
			b.log.Debug("Could not mark clear cancelled", "err", err)
		}
		return b.answer(cb.ctx, cb.queryID, "Cancelled", false)
	case "confirm":
	default:
		return b.answer(cb.ctx, cb.queryID, "", false)
	}

	failed := false
	if len(session.queueIDs) > 0 {
		if _, err := b.nzb.EditQueue(cb.ctx, "GroupFinalDelete", "", session.queueIDs); err != nil {
			b.log.Error("clear confirm RPC failed", "err", err)
			failed = true
		}
	}
	if !failed && len(session.historyIDs) > 0 {
		if _, err := b.nzb.EditQueue(cb.ctx, "HistoryFinalDelete", "", session.historyIDs); err != nil {
			b.log.Error("clear confirm RPC failed", "err", err)
			failed = true
		}
	}
	if !failed {
		b.forgetTasks(append(append([]int64{}, session.queueIDs...), session.historyIDs...))
	}

	b.takeClear(token)

	result := "Purged"
	if failed {
		result += "\nRPC error occurred during deletion"
	}
	if err := b.edit(cb.ctx, cb.peer, cb.msgID, result); err != nil {
		b.log.Debug("Could not report clear result", "err", err)
	}
	return b.answer(cb.ctx, cb.queryID, "Clear completed", false)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
