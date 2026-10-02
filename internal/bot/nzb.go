package bot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/crypt"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/nzbget"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/util"
)

// statusPollInterval is how often a live /status message is refreshed.
const statusPollInterval = 10 * time.Second

// statusTaskLimit is how many queue entries a /status message lists.
const statusTaskLimit = 6

// idSeparators splits a list of IDs pasted with spaces, commas or newlines.
var idSeparators = regexp.MustCompile(`[ \n,]+`)

func escape(s string) string { return util.Escape(s) }

// handleGrab adds downloads, either from a replied .nzb file or from the NZB
// IDs shown under search results.
func (b *Bot) handleGrab(c *Ctx) error {
	fromFile, err := b.grabFromFile(c)
	if err != nil {
		b.log.Error("Error while adding", "err", err)
		_, replyErr := c.Reply(b.failureText("Error adding NZB file"))
		return replyErr
	}

	fromIDs, err := b.grabFromIDs(c)
	if err != nil {
		b.log.Error("Error while adding", "err", err)
		_, replyErr := c.Reply(b.failureText("Error adding NZB file"))
		return replyErr
	}

	if !fromFile && !fromIDs {
		_, err := c.Reply("Provide a proper NZB ID or reply to a .NZB file")
		return err
	}
	return nil
}

// grabFromFile handles /grab sent as a reply to an .nzb document. It reports
// whether the reply was one, not whether the add succeeded.
func (b *Bot) grabFromFile(c *Ctx) (bool, error) {
	replied, err := b.repliedMessage(c)
	if err != nil {
		// A failed lookup is not "no reply": reporting it as one tells the
		// user their NZB ID was wrong when Telegram was the problem.
		return true, fmt.Errorf("fetch replied message: %w", err)
	}
	if replied == nil {
		return false, nil
	}

	document, filename, ok := nzbDocument(replied)
	if !ok {
		return false, nil
	}

	target, err := c.Peer()
	if err != nil {
		return true, err
	}

	// A private temp file, never the attachment's own name: two grabs of
	// "foo.nzb" at once would otherwise share a path, and a crafted name
	// could point outside the temp directory.
	tmp, err := os.CreateTemp("", "nzb-*.nzb")
	if err != nil {
		return true, fmt.Errorf("create temp file: %w", err)
	}
	filePath := tmp.Name()
	tmp.Close()
	defer func() {
		if err := os.Remove(filePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			b.log.Error("Error while removing downloaded NZB", "path", filePath, "err", err)
		}
	}()

	// Empty thumb size: the file itself, not a preview.
	if _, err := b.downloader.Download(b.api, document.AsInputDocumentFileLocation("")).ToPath(c.ctx, filePath); err != nil {
		return true, fmt.Errorf("download %s: %w", path.Base(filename), err)
	}

	notice, err := c.Reply("Adding .NZB file")
	if err != nil {
		return true, err
	}

	// The attachment's own name, not the temp path: NZBGet names the job
	// after it, and the temp name would show up as "nzb-1234567890".
	nzbID, appendErr := b.nzb.AppendFile(c.ctx, filePath, path.Base(filename))
	if appendErr != nil {
		if err := b.delete(c.ctx, target, notice.ID); err != nil {
			b.log.Debug("Could not delete notice", "err", err)
		}
		return true, appendErr
	}

	b.rememberTask(nzbID, c.userID)
	name := b.nzbName(c.ctx, nzbID)

	if err := b.delete(c.ctx, target, notice.ID); err != nil {
		b.log.Error("Could not delete notice", "err", err)
	}
	if _, err := c.Reply(addedText([]string{name}, c.userID, c.FirstName())); err != nil {
		return true, err
	}
	return true, b.runStatus(c)
}

// addedText renders the confirmation both add paths send, so a download queued
// from an attachment is reported the same way as one queued from an NZB ID.
func addedText(names []string, userID int64, firstName string) string {
	list := escape(strings.Join(names, "\n\n"))
	return fmt.Sprintf("<u><b>NZB ADDED</b></u>\n\n<code>%s</code>\n\nby %s",
		list, mention(userID, firstName))
}

// nzbName reads a queued download's name back from NZBGet.
func (b *Bot) nzbName(ctx context.Context, nzbID int64) string {
	groups, err := b.nzb.ListGroups(ctx)
	if err != nil {
		b.log.Error("Could not read back NZB name", "nzb_id", nzbID, "err", err)
		return fmt.Sprintf("Unable to retrieve NZB name for: '%d'", nzbID)
	}
	for _, group := range groups {
		if group.NZBID == nzbID {
			return group.NZBName
		}
	}
	return fmt.Sprintf("Unable to retrieve NZB name for: '%d'", nzbID)
}

// nzbDocument reports whether a message carries an .nzb attachment.
func nzbDocument(msg *tg.Message) (*tg.Document, string, bool) {
	media, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return nil, "", false
	}
	document, ok := media.Document.(*tg.Document)
	if !ok {
		return nil, "", false
	}
	if !strings.Contains(document.MimeType, "application") {
		return nil, "", false
	}

	var filename string
	for _, attribute := range document.Attributes {
		if name, ok := attribute.(*tg.DocumentAttributeFilename); ok {
			filename = name.FileName
			break
		}
	}
	// Case-insensitive: Telegram keeps whatever the sender named the file, so
	// "Something.NZB" would otherwise be rejected and /grab would fall through
	// to telling the user to provide an ID.
	if !strings.HasSuffix(strings.ToLower(filename), ".nzb") {
		return nil, "", false
	}
	return document, filename, true
}

// repliedMessage fetches the message a command replied to, if any.
func (b *Bot) repliedMessage(c *Ctx) (*tg.Message, error) {
	header, ok := c.msg.ReplyTo.(*tg.MessageReplyHeader)
	if !ok {
		return nil, nil
	}
	repliedID, ok := header.GetReplyToMsgID()
	if !ok {
		return nil, nil
	}

	target, err := c.Peer()
	if err != nil {
		return nil, err
	}

	var result tg.MessagesMessagesClass
	if channel, isChannel := peer.ToInputChannel(target); isChannel {
		result, err = b.api.ChannelsGetMessages(c.ctx, &tg.ChannelsGetMessagesRequest{
			Channel: channel,
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: repliedID}},
		})
	} else {
		result, err = b.api.MessagesGetMessages(c.ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: repliedID}})
	}
	if err != nil {
		return nil, err
	}

	messages, ok := result.AsModified()
	if !ok {
		return nil, nil
	}
	for _, candidate := range messages.GetMessages() {
		if msg, ok := candidate.(*tg.Message); ok {
			return msg, nil
		}
	}
	return nil, nil
}

// errInvalidNZBID marks a pasted token that is not a usable NZB ID.
//
// Bad base64, bytes that are not UTF-8, a missing "|", an unknown site -- from
// the user's side these are all one thing: the ID is wrong. Reporting them as
// one kind lets them be listed together under INVALID NZB IDS.
var errInvalidNZBID = errors.New("invalid NZB ID")

// decodeNZBID turns a displayed NZB ID back into its result ID and site.
func (b *Bot) decodeNZBID(token string) (resultID, site string, err error) {
	plaintext, err := crypt.Decrypt(token)
	if err != nil {
		return "", "", fmt.Errorf("%w: could not decode (%v)", errInvalidNZBID, err)
	}
	resultID, site, found := strings.Cut(plaintext, "|")
	if !found || strings.Contains(site, "|") {
		return "", "", fmt.Errorf("%w: decoded text is not '<id>|<site>'", errInvalidNZBID)
	}
	if _, ok := b.idx.Get(site); !ok {
		return "", "", fmt.Errorf("%w: unknown site %q", errInvalidNZBID, site)
	}
	return resultID, site, nil
}

// grabFromIDs adds every NZB ID listed after the command. It reports whether
// the command carried any IDs at all.
func (b *Bot) grabFromIDs(c *Ctx) (bool, error) {
	if c.args == "" {
		return false, nil
	}
	// Empty tokens come from "a, b" and the like; they are separators, not IDs
	// the user got wrong.
	var ids []string
	for _, token := range idSeparators.Split(c.args, -1) {
		if strings.TrimSpace(token) != "" {
			ids = append(ids, token)
		}
	}
	if len(ids) == 0 {
		return false, nil
	}

	var added, failed []string
	for _, token := range ids {
		resultID, site, err := b.decodeNZBID(token)
		if err != nil {
			// A mistyped or truncated ID is user input, not a fault, so it
			// joins the consolidated reply and is logged as a one-liner.
			b.log.Info("Invalid NZB ID", "token", token, "reason", err)
			failed = append(failed, token)
			continue
		}

		name, err := b.appendResult(c, resultID, site)
		if err != nil {
			b.log.Error("Error while adding", "token", token, "err", err)
			failed = append(failed, token)
			continue
		}
		added = append(added, name)
	}

	if len(added) > 0 {
		if _, err := c.Reply(addedText(added, c.userID, c.FirstName())); err != nil {
			return true, err
		}
	}

	if len(failed) > 0 {
		list := escape(strings.Join(failed, "\n"))
		if _, err := c.Reply(fmt.Sprintf("<u><b>INVALID NZB IDS</b></u>\n\n<code>%s</code>", list)); err != nil {
			return true, err
		}
	}

	if len(failed) == len(ids) {
		return true, nil
	}
	return true, b.runStatus(c)
}

// appendResult queues one decoded search result and returns its NZB name.
func (b *Bot) appendResult(c *Ctx, resultID, site string) (string, error) {
	indexer, ok := b.idx.Get(site)
	if !ok {
		return "", fmt.Errorf("unknown site %q", site)
	}

	var (
		nzbID int64
		err   error
	)
	if site == "nzbhydra" && indexer.Internal {
		nzbID, err = b.appendInternal(c.ctx, resultID, indexer.APIURL)
	} else {
		nzbID, err = b.nzb.AppendURL(c.ctx, indexer.DownloadURL(resultID))
	}
	if err != nil {
		return "", err
	}

	b.rememberTask(nzbID, c.userID)
	return b.nzbName(c.ctx, nzbID), nil
}

// appendInternal hands the result to NZBHydra's downloader integration, then
// finds the NZBID NZBGet assigned to it.
func (b *Bot) appendInternal(ctx context.Context, resultID, apiURL string) (int64, error) {
	ok, err := b.idx.AddInternal(ctx, resultID, apiURL, b.cfg.DownloaderName)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errors.New("failed adding internally")
	}

	// NZBHydra hands the NZB to NZBGet on its own schedule, so the queue may
	// not list it on the first look.
	for attempt := 0; ; attempt++ {
		groups, err := b.nzb.ListGroups(ctx)
		if err != nil {
			return 0, err
		}

		// Newest match, not the first. Grabbing the same NZB again while the
		// earlier copy is still unpacking leaves two entries with the same
		// URL; returning the older one records the wrong NZBID against the
		// user, so the download they just started could not be cancelled by
		// them.
		var newest int64
		for _, group := range groups {
			if urlReferencesID(group.URL, resultID) && group.NZBID > newest {
				newest = group.NZBID
			}
		}
		if newest != 0 {
			return newest, nil
		}
		if attempt >= nzbIDLookupRetries {
			return 0, fmt.Errorf("NZBID not found for: %s", resultID)
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(nzbIDLookupInterval):
		}
	}
}

// nzbIDLookupRetries and nzbIDLookupInterval bound how long appendInternal
// waits for NZBHydra's hand-off to show up in the NZBGet queue.
const (
	nzbIDLookupRetries  = 5
	nzbIDLookupInterval = time.Second
)

// urlReferencesID reports whether a queue entry's URL carries id as a whole
// query value or path segment. A substring test matched result 123 against
// result 1234 and recorded the wrong download against the user.
func urlReferencesID(rawURL, id string) bool {
	if id == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	for _, values := range parsed.Query() {
		for _, value := range values {
			if value == id {
				return true
			}
		}
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == id {
			return true
		}
	}
	return false
}

// failureText is the reply for a handler that failed: the given text, unless
// the failure was the bot itself being restarted.
func (b *Bot) failureText(text string) string {
	if b.interrupted() {
		return errInterrupted
	}
	return text
}

func (b *Bot) rememberTask(nzbID, userID int64) {
	b.mu.Lock()
	b.tasks[nzbID] = userID
	b.mu.Unlock()
	if err := b.store.AddTask(nzbID, userID); err != nil {
		b.log.Error("Could not record task owner", "nzb_id", nzbID, "err", err)
	}
}

func (b *Bot) forgetTasks(nzbIDs []int64) {
	b.mu.Lock()
	for _, id := range nzbIDs {
		delete(b.tasks, id)
	}
	b.mu.Unlock()
	if err := b.store.RemoveTasks(nzbIDs); err != nil {
		b.log.Error("Could not forget task owners", "err", err)
	}
}

func (b *Bot) taskOwner(nzbID int64) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	owner, ok := b.tasks[nzbID]
	return owner, ok
}

// statusSession is a live /status message being refreshed in a chat.
type statusSession struct {
	msgID  int
	cancel context.CancelFunc
}

func (b *Bot) handleStatus(c *Ctx) error {
	return b.runStatus(c)
}

// runStatus replaces any previous /status message in the chat and then keeps
// the new one up to date until the queue drains.
func (b *Bot) runStatus(c *Ctx) error {
	target, err := c.Peer()
	if err != nil {
		return err
	}

	b.mu.Lock()
	previous := b.statuses[c.chatID]
	delete(b.statuses, c.chatID)
	b.mu.Unlock()

	if previous != nil {
		previous.cancel()
		if err := b.delete(c.ctx, target, previous.msgID); err != nil {
			b.log.Debug("Could not delete previous status message", "err", err)
		}
	}

	reply, err := c.Reply("Fetching status")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(c.ctx)
	session := &statusSession{msgID: reply.ID, cancel: cancel}

	b.mu.Lock()
	b.statuses[c.chatID] = session
	b.mu.Unlock()

	go func() {
		defer cancel()
		defer b.recoverPanic("status loop")
		b.pollStatus(ctx, target, c.chatID, session)
	}()
	return nil
}

func (b *Bot) pollStatus(ctx context.Context, target tg.InputPeerClass, chat int64, session *statusSession) {
	defer b.endStatus(chat, session)

	first := true
	editFailures := 0

	for {
		serverStatus, err := b.nzb.Status(ctx)
		if err != nil {
			b.log.Error("Error while fetching status", "err", err)
			b.edit(ctx, target, session.msgID, "Error fetching status")
			return
		}
		groups, err := b.nzb.ListGroups(ctx)
		if err != nil {
			b.log.Error("Error while fetching status", "err", err)
			b.edit(ctx, target, session.msgID, "Error fetching status")
			return
		}

		if len(groups) == 0 {
			if first {
				b.edit(ctx, target, session.msgID, "No active tasks")
				return
			}
			// Queue drained -- drop the message and stop polling rather than
			// keep editing an empty body, which Telegram rejects outright.
			if err := b.delete(ctx, target, session.msgID); err != nil {
				b.log.Debug("Could not delete drained status message", "err", err)
			}
			return
		}
		first = false

		text := renderStatus(groups, serverStatus.DownloadRate)
		switch err := b.edit(ctx, target, session.msgID, text); {
		case err == nil, tgerr.Is(err, "MESSAGE_NOT_MODIFIED"):
			editFailures = 0
		case tgerr.Is(err, "MESSAGE_ID_INVALID"), tgerr.Is(err, "MESSAGE_DELETE_FORBIDDEN"):
			return
		case errors.Is(err, context.Canceled):
			b.reportInterrupted(ctx, target, session.msgID)
			return
		default:
			// Backstop: give up rather than poll forever on an error we did
			// not anticipate.
			editFailures++
			b.log.Error("Could not update status message", "err", err)
			if editFailures >= 3 {
				return
			}
		}

		select {
		case <-ctx.Done():
			b.reportInterrupted(ctx, target, session.msgID)
			return
		case <-time.After(statusPollInterval):
		}
	}
}

// reportInterrupted marks a live status message as cut short by a restart.
// A status replaced by a newer /status is cancelled too, but that one is
// deleted by its successor, so only shutdown is worth announcing.
func (b *Bot) reportInterrupted(ctx context.Context, target tg.InputPeerClass, msgID int) {
	if !b.interrupted() {
		return
	}
	if err := b.edit(ctx, target, msgID, errInterrupted); err != nil {
		b.log.Debug("Could not mark status interrupted", "err", err)
	}
}

// endStatus clears the chat's session, unless a newer /status already replaced
// it.
func (b *Bot) endStatus(chat int64, session *statusSession) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.statuses[chat] == session {
		delete(b.statuses, chat)
	}
}

func renderStatus(groups []nzbget.Group, speed int64) string {
	var b strings.Builder
	limit := min(len(groups), statusTaskLimit)

	for _, group := range groups[:limit] {
		state := group.Status
		health := formatHealth(group)

		var stats string
		switch state {
		case "DOWNLOADING":
			stats = downloadStats(group, speed, health)
		case "EXECUTING_SCRIPT":
			state = "UPLOADING"
			stats = transferStats(group, health)
		default:
			stats = otherStats(group, health)
		}

		name := escape(group.NZBName)
		if state == "FETCHING" {
			fmt.Fprintf(&b, "\n\n<u><b>%s</b></u>\n<code>%s</code>\n<code>%s</code>\n<code>/cancel %d</code>",
				state, name, health, group.NZBID)
			continue
		}
		fmt.Fprintf(&b, "\n\n<u><b>%s</b></u>\n<code>%s\n</code>%s<code>/cancel %d</code>",
			state, name, stats, group.NZBID)
	}

	if extra := len(groups) - statusTaskLimit; extra > 0 {
		fmt.Fprintf(&b, "\n\n<b>+%d more task(s)</b>", extra)
	}
	return b.String()
}

func formatHealth(group nzbget.Group) string {
	if group.Health == 0 {
		return ""
	}
	return fmt.Sprintf("H: %.1f%%", float64(group.Health)/10)
}

func withHealth(stats, health string) string {
	if health != "" {
		stats += fmt.Sprintf(" <code>|</code> <code>%s</code>", health)
	}
	return stats + "\n"
}

func downloadStats(group nzbget.Group, speed int64, health string) string {
	// NZBGet reports DOWNLOADING before it has read the file list, so the total
	// is briefly 0. Dividing there would take the whole status message down,
	// including the other tasks that are rendering fine.
	var progress float64
	if group.FileSize > 0 {
		progress = float64(group.DownloadedSize) / float64(group.FileSize) * 100
	}
	var eta int64
	if speed > 0 {
		eta = (group.FileSize - group.DownloadedSize) / speed
	}

	stats := fmt.Sprintf("<code>%s</code>\n", util.ProgressBar(progress))
	stats += fmt.Sprintf("<code>%s</code> <code>/</code> <code>%s</code>\n",
		util.ConvertSize(group.DownloadedSize), util.ConvertSize(group.FileSize))
	stats += fmt.Sprintf("↓<code>%s/s</code> <code>|</code> <code>ETA</code> : <code>%s</code>",
		util.ConvertSize(speed), util.TimeFormat(eta))
	return withHealth(stats, health)
}

// transferPattern reads NZBGet's free-form post-processing progress line, e.g.
// "1.2 GB / 4.5 GB, 27%, 8.1 MB/s, ETA 5m30s".
var transferPattern = regexp.MustCompile(
	`(\d+(?:\.\d+)?)\s*(\w+)\s*/\s*(\d+(?:\.\d+)?)\s*(\w+),\s*([\d.]+)%,\s*([\d.]+)\s*(\w+)/s,\s*ETA\s*([\dwdhms]+)`)

func transferStats(group nzbget.Group, health string) string {
	match := transferPattern.FindStringSubmatch(group.PostInfoText)
	if match == nil {
		stats := fmt.Sprintf("<code>%s</code>\n", util.ProgressBar(0))
		stats += "↑<code>0B/s</code> <code>|</code> <code>ETA</code> : <code>N/A</code>"
		return withHealth(stats, health)
	}

	percentage, _ := strconv.ParseFloat(match[5], 64)
	stats := fmt.Sprintf("<code>%s</code>\n", util.ProgressBar(percentage))
	stats += fmt.Sprintf("<code>%s %s</code> <code>/</code> <code>%s %s</code>\n",
		match[1], match[2], match[3], match[4])
	stats += fmt.Sprintf("↑<code>%s%s/s</code> <code>|</code> <code>ETA</code> : <code>%s</code>",
		match[6], match[7], match[8])
	return withHealth(stats, health)
}

// otherStats derives a bar for the post-processing stages, which report only a
// per-mille progress and the seconds spent so far.
func otherStats(group nzbget.Group, health string) string {
	if group.PostStageProgress == 0 || group.PostStageTimeSec == 0 {
		return ""
	}
	percentage := float64(group.PostStageProgress) / 10
	processed := percentage / 100 * float64(group.FileSize)
	speed := processed / float64(group.PostStageTimeSec)
	var eta int64
	if speed > 0 {
		eta = int64((float64(group.FileSize) - processed) / speed)
	}

	stats := fmt.Sprintf("<code>%s</code>\n", util.ProgressBar(percentage))
	stats += fmt.Sprintf("<code>%s</code> <code>/</code> <code>%s</code>\n",
		util.ConvertSize(processed), util.ConvertSize(group.FileSize))
	stats += fmt.Sprintf("<code>%s/s</code> <code>|</code> <code>ETA</code> : <code>%s</code>",
		util.ConvertSize(speed), util.TimeFormat(eta))
	return withHealth(stats, health)
}

// handleCancel removes downloads the requester owns, or any download when an
// admin asks.
func (b *Bot) handleCancel(c *Ctx) error {
	if c.args == "" {
		_, err := c.Reply("Invalid ID")
		return err
	}

	var requested []int64
	for _, token := range idSeparators.Split(c.args, -1) {
		if strings.TrimSpace(token) == "" {
			continue
		}
		id, err := strconv.ParseInt(token, 10, 64)
		if err != nil {
			_, replyErr := c.Reply("Invalid ID")
			return replyErr
		}
		requested = append(requested, id)
	}
	if len(requested) == 0 {
		_, err := c.Reply("Invalid ID")
		return err
	}

	isAdmin := b.isAdmin(c.userID)
	seen := map[int64]bool{}
	var ids []int64
	for _, id := range requested {
		if seen[id] {
			continue
		}
		seen[id] = true
		if owner, ok := b.taskOwner(id); isAdmin || (ok && owner == c.userID) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		_, err := c.Reply("Not your task")
		return err
	}

	queueDeleted, err := b.nzb.EditQueue(c.ctx, "GroupFinalDelete", "", ids)
	if err != nil {
		b.log.Error("Error while cancelling", "err", err)
		_, replyErr := c.Reply("Invalid ID")
		return replyErr
	}
	postDeleted, err := b.nzb.EditQueue(c.ctx, "PostDelete", "", ids)
	if err != nil {
		b.log.Error("Error while cancelling", "err", err)
		_, replyErr := c.Reply("Invalid ID")
		return replyErr
	}

	if !queueDeleted && !postDeleted {
		_, err := c.Reply("Invalid ID")
		return err
	}

	b.forgetTasks(ids)
	_, err = c.Reply("Cancelled task")
	return err
}
