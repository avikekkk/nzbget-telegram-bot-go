package bot

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gotd/td/telegram/message"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/mem"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/util"
)

// startTime is when the process came up, for the /server uptime line.
var startTime = time.Now()

// handleServer reports NZBGet's counters alongside the bot host's own.
func (b *Bot) handleServer(c *Ctx) error {
	status, err := b.nzb.Status(c.ctx)
	if err != nil {
		return err
	}

	usage, err := disk.Usage("/")
	if err != nil {
		return err
	}
	memory, err := mem.VirtualMemory()
	if err != nil {
		return err
	}
	cpuPercents, err := cpu.Percent(500*time.Millisecond, false)
	if err != nil {
		return err
	}
	cpuPercent := 0.0
	if len(cpuPercents) > 0 {
		cpuPercent = cpuPercents[0]
	}

	// NZBGet's own disk is distinct from the one under SYSTEM STATS below,
	// which is whatever host the bot runs on -- a different machine unless the
	// two sit side by side.
	text := "<u><b>NZBGET STATS</b></u>\n" +
		fmt.Sprintf("<code>Avg Speed:</code> <code>%s/s</code>\n", util.ConvertSize(status.AverageDownloadRate)) +
		fmt.Sprintf("<code>NZBGet Uptime:</code> <code>%s</code>\n", util.ReadableTime(status.UpTimeSec)) +
		fmt.Sprintf("<code>Disk:</code> <code>%s</code> <code>|</code> <code>Free:</code> <code>%s</code>\n\n",
			util.ConvertSize(status.TotalDiskSpace), util.ConvertSize(status.FreeDiskSpace)) +
		"<u><b>SYSTEM STATS</b></u>\n" +
		fmt.Sprintf("<code>Bot Uptime:</code> <code>%s</code>\n", util.ReadableTime(int64(time.Since(startTime).Seconds()))) +
		fmt.Sprintf("<code>Disk:</code> <code>%s</code> <code>|</code> <code>Free:</code> <code>%s</code>\n\n",
			util.ConvertSize(int64(usage.Total)), util.ConvertSize(int64(usage.Free))) +
		fmt.Sprintf("<code>CPU:</code> <code>%s%%</code> <code>|</code> <code>RAM:</code> <code>%s%%</code> <code>|</code> <code>DISK:</code> <code>%s%%</code>",
			percent(cpuPercent), percent(memory.UsedPercent), percent(usage.UsedPercent))

	_, err = c.Reply(text)
	return err
}

// percent renders a usage figure to one decimal place. psutil handed back
// values already rounded that way; Go's counters carry their full float
// precision, which reads as "2.1702838064197483%" if printed as-is.
func percent(value float64) string {
	return strconv.FormatFloat(value, 'f', 1, 64)
}

// handleLogs uploads the bot's log file.
func (b *Bot) handleLogs(c *Ctx) error {
	upload, err := b.uploader.FromPath(c.ctx, b.cfg.LogFile)
	if err != nil {
		return err
	}
	_, err = b.sender.Reply(c.ents, c.upd).Media(c.ctx,
		message.UploadedDocument(upload).
			Filename("log.txt").
			MIME("text/plain").
			ForceFile(true))
	return err
}

// targetID resolves who an /auth or /unauth applies to: the replied-to user, a
// numeric argument, or the chat the command was sent in.
func (b *Bot) targetID(c *Ctx) (id int64, isUser bool, ok bool) {
	if replied, err := b.repliedMessage(c); err == nil && replied != nil {
		if userID := senderID(replied); userID != 0 {
			return userID, true, true
		}
	}
	if c.args != "" {
		field := c.args
		if space := indexSpace(field); space >= 0 {
			field = field[:space]
		}
		parsed, err := strconv.ParseInt(field, 10, 64)
		if err != nil {
			return 0, false, false
		}
		return parsed, false, true
	}
	return c.chatID, false, true
}

func indexSpace(s string) int {
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			return i
		}
	}
	return -1
}

func (b *Bot) handleAuth(c *Ctx) error {
	target, isUser, ok := b.targetID(c)
	if !ok {
		_, err := c.Reply("Please provide a valid numeric ID.")
		return err
	}

	var text string
	switch {
	case b.isAdmin(target), b.chatAuthorized(target):
		text = "Already authorized!"
	default:
		b.mu.Lock()
		b.authChats[target] = true
		b.mu.Unlock()
		if err := b.store.AddAuthChat(target); err != nil {
			b.log.Error("Could not store authorized chat", "chat_id", target, "err", err)
		}
		text = fmt.Sprintf("Authorized <code>%d</code>", target)
	}
	if isUser {
		text += " [User]"
	}
	_, err := c.Reply(text)
	return err
}

func (b *Bot) handleUnauth(c *Ctx) error {
	target, isUser, ok := b.targetID(c)
	if !ok {
		_, err := c.Reply("Please provide a valid numeric ID.")
		return err
	}

	var text string
	switch {
	case b.isAdmin(target):
		text = "Bot admin!"
	case !b.chatAuthorized(target):
		text = "Already unauthorized!"
	default:
		b.mu.Lock()
		delete(b.authChats, target)
		b.mu.Unlock()
		if err := b.store.RemoveAuthChat(target); err != nil {
			b.log.Error("Could not remove authorized chat", "chat_id", target, "err", err)
		}
		text = fmt.Sprintf("Unauthorized <code>%d</code>", target)
	}
	if isUser {
		text += " [User]"
	}
	_, err := c.Reply(text)
	return err
}

func (b *Bot) chatAuthorized(chatID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authChats[chatID]
}

const helpText = `<u><b>BOT COMMANDS</b></u>

<b>✦ To search for NZBs:</b>

<code>/search [query]</code>

<b>✦ Sort flags:</b>

The flags <code>--mx</code> and <code>--mn</code> are optional and must be the last word of a <code>/search</code>.

<code>  --mx    [Largest first]
  --mn    [Smallest first]</code>

Without a flag, results come back newest first.

<b>✦ To add downloads from search results:</b>

<code>/grab [ID-1] [ID-2]...</code>

These IDs are the NZB IDs shown under each search result, separated by spaces, commas or new lines.

<b>✦ To add a download by replying to an NZB file:</b>

<code>/grab</code>

<b>✦ To view the status:</b>

<code>/server    [displays the server stats]
/status    [displays the download progress]</code>

<b>✦ To cancel a download:</b>

<code>/cancel [ID-1] [ID-2]...</code>

These IDs come from <code>/status</code>, not from search results. You can only cancel tasks you started.

<b>✦ Admin only:</b>

<code>/logs            [bot log file]
/clear           [wipe NZBGet queue and history]
/auth [id]       [authorize a user or chat]
/unauth [id]     [remove authorization]</code>`

func (b *Bot) handleHelp(c *Ctx) error {
	_, err := c.Reply(helpText)
	return err
}
