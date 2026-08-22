// Package bot wires the Telegram client to the NZBGet handlers.
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"unicode"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/avikekkk/nzbget-usenet-bot-go/internal/config"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/indexers"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/nzbget"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/store"
	"github.com/avikekkk/nzbget-usenet-bot-go/internal/telegraph"
)

// Bot holds everything the handlers need.
type Bot struct {
	cfg   *config.Config
	log   *slog.Logger
	store *store.Store
	nzb   *nzbget.Client
	idx   *indexers.Set

	api        *tg.Client
	sender     *message.Sender
	uploader   *uploader.Uploader
	downloader *downloader.Downloader
	username   string

	// runCtx outlives an individual update, so handlers spawned as goroutines
	// keep working after the dispatcher returns.
	runCtx context.Context

	mu        sync.Mutex
	tasks     map[int64]int64 // NZBID -> owner user ID
	authChats map[int64]bool
	statuses  map[int64]*statusSession // chat ID -> live /status message

	// Sessions are capped, and the order slices give the map an insertion
	// order to evict by.
	searches map[string]*searchSession
	// One Telegraph account is shared by every search; see telegraphAccount.
	telegraphMu sync.Mutex
	telegraph   *telegraph.Client
	searchOrder []string
	clears      map[string]*clearSession
	clearOrder  []string
}

// New builds a bot from configuration and its backing services.
func New(cfg *config.Config, logger *slog.Logger, db *store.Store, nzb *nzbget.Client) *Bot {
	b := &Bot{
		cfg:       cfg,
		log:       logger,
		store:     db,
		nzb:       nzb,
		idx:       indexers.New(cfg),
		tasks:     map[int64]int64{},
		authChats: map[int64]bool{},
		statuses:  map[int64]*statusSession{},
		searches:  map[string]*searchSession{},
		clears:    map[string]*clearSession{},
	}
	for _, chatID := range cfg.AuthChats {
		b.authChats[chatID] = true
	}
	return b
}

// Run connects to Telegram and serves updates until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) error {
	dispatcher := tg.NewUpdateDispatcher()
	dispatcher.OnNewMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewMessage) error {
		b.onMessage(e, u)
		return nil
	})
	dispatcher.OnNewChannelMessage(func(ctx context.Context, e tg.Entities, u *tg.UpdateNewChannelMessage) error {
		b.onMessage(e, u)
		return nil
	})
	dispatcher.OnBotCallbackQuery(func(ctx context.Context, e tg.Entities, u *tg.UpdateBotCallbackQuery) error {
		b.onCallback(e, u)
		return nil
	})

	gaps := updates.New(updates.Config{Handler: dispatcher})

	client := telegram.NewClient(b.cfg.APIID, b.cfg.APIHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: b.cfg.SessionFile},
		UpdateHandler:  gaps,
	})

	return client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Auth().Bot(ctx, b.cfg.BotToken); err != nil {
			return fmt.Errorf("bot authorization: %w", err)
		}

		self, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("self: %w", err)
		}

		b.runCtx = ctx
		b.api = client.API()
		b.sender = message.NewSender(b.api)
		b.uploader = uploader.NewUploader(b.api)
		b.downloader = downloader.NewDownloader()
		b.username = self.Username

		if err := b.loadState(ctx); err != nil {
			return err
		}

		b.log.Info("Telegram client initialised", "bot", "@"+self.Username)

		return gaps.Run(ctx, b.api, self.ID, updates.AuthOptions{IsBot: true})
	})
}

// loadState restores the authorized chats and task ownership from the
// database. Rows for downloads NZBGet no longer knows about are dropped on the
// way in, so the table tracks the live queue and history rather than growing
// forever.
func (b *Bot) loadState(ctx context.Context) error {
	stored, err := b.store.LoadAuthChats()
	if err != nil {
		return fmt.Errorf("load authorized chats: %w", err)
	}
	added := 0
	for _, chatID := range stored {
		if !b.authChats[chatID] {
			b.authChats[chatID] = true
			added++
		}
	}
	if added > 0 {
		b.log.Info("Loaded authorized chats from database", "count", added)
	}

	tasks, err := b.store.LoadTasks()
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	b.tasks = tasks
	if len(tasks) == 0 {
		return nil
	}

	known := map[int64]bool{}
	groups, err := b.nzb.ListGroups(ctx)
	if err != nil {
		b.log.Error("Could not prune stale task owners", "err", err)
		return nil
	}
	for _, group := range groups {
		known[group.NZBID] = true
	}
	history, err := b.nzb.History(ctx, false)
	if err != nil {
		b.log.Error("Could not prune stale task owners", "err", err)
		return nil
	}
	for _, item := range history {
		known[item.NZBID] = true
	}

	var stale []int64
	for nzbID := range b.tasks {
		if !known[nzbID] {
			stale = append(stale, nzbID)
			delete(b.tasks, nzbID)
		}
	}
	if err := b.store.RemoveTasks(stale); err != nil {
		b.log.Error("Could not remove stale task owners", "err", err)
	}
	b.log.Info("Loaded task owners from database", "loaded", len(b.tasks), "dropped", len(stale))
	return nil
}

// onMessage routes an incoming message to its command handler.
func (b *Bot) onMessage(e tg.Entities, upd message.AnswerableMessageUpdate) {
	msg, ok := upd.GetMessage().(*tg.Message)
	if !ok || msg.Out {
		return
	}

	command, args, ok := b.parseCommand(msg.Message)
	if !ok {
		return
	}

	c := &Ctx{
		ctx:     b.runCtx,
		bot:     b,
		ents:    e,
		upd:     upd,
		msg:     msg,
		chatID:  chatID(msg.PeerID),
		userID:  senderID(msg),
		command: command,
		args:    args,
	}
	if c.userID == 0 {
		return
	}

	handler, sudoOnly, known := b.route(command)
	if !known {
		return
	}
	if sudoOnly {
		if !b.isAdmin(c.userID) {
			return
		}
	} else if !b.isAuthorized(c.chatID, c.userID) {
		return
	}

	// Handlers block on NZBGet and on Telegram; running them inline would stall
	// the update loop for every other chat.
	go func() {
		defer b.recoverPanic("command " + command)
		if err := handler(c); err != nil {
			b.log.Error("Handler failed", "command", command, "err", err)
		}
	}()
}

type handlerFunc func(*Ctx) error

// route maps a command to its handler and whether it is admin-only.
func (b *Bot) route(command string) (handlerFunc, bool, bool) {
	switch command {
	case "help":
		return b.handleHelp, false, true
	case "grab":
		return b.handleGrab, false, true
	case "status":
		return b.handleStatus, false, true
	case "cancel":
		return b.handleCancel, false, true
	case "search":
		return b.handleSearch, false, true
	case "server":
		return b.handleServer, false, true
	case "logs":
		return b.handleLogs, true, true
	case "clear":
		return b.handleClear, true, true
	case "auth":
		return b.handleAuth, true, true
	case "unauth":
		return b.handleUnauth, true, true
	default:
		return nil, false, false
	}
}

// parseCommand splits "/cmd@thisbot rest" into its command and arguments. A
// command addressed to a different bot in a group is not ours to answer.
func (b *Bot) parseCommand(text string) (command, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	// The command word ends at the first whitespace of any kind. Splitting on a
	// literal space instead loses the arguments of "/grab\n<id>\n<id>", which is
	// how a list of IDs pastes out of a search result.
	body := text[1:]
	head, rest := body, ""
	if cut := strings.IndexFunc(body, unicode.IsSpace); cut >= 0 {
		head, rest = body[:cut], body[cut:]
	}
	if head == "" {
		return "", "", false
	}
	if name, target, found := strings.Cut(head, "@"); found {
		if !strings.EqualFold(target, b.username) {
			return "", "", false
		}
		head = name
	}
	return strings.ToLower(head), strings.TrimSpace(rest), true
}

func (b *Bot) isAdmin(userID int64) bool {
	for _, admin := range b.cfg.Admins {
		if admin == userID {
			return true
		}
	}
	return false
}

func (b *Bot) isAuthorized(chatID, userID int64) bool {
	if b.isAdmin(userID) {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.authChats[chatID]
}

func (b *Bot) recoverPanic(what string) {
	if r := recover(); r != nil {
		b.log.Error("Recovered from panic", "where", what, "panic", r)
	}
}

// chatID renders a peer the way the Python bot did, so the IDs in config.env
// and in the database keep working: users as-is, basic groups negated, and
// supergroups/channels offset below -1000000000000.
func chatID(p tg.PeerClass) int64 {
	switch peer := p.(type) {
	case *tg.PeerUser:
		return peer.UserID
	case *tg.PeerChat:
		return -peer.ChatID
	case *tg.PeerChannel:
		return -1000000000000 - peer.ChannelID
	default:
		return 0
	}
}

// senderID is the user who sent a message. In private chats Telegram omits
// from_id, because the sender is the chat itself.
func senderID(msg *tg.Message) int64 {
	if from, ok := msg.GetFromID(); ok {
		if user, ok := from.(*tg.PeerUser); ok {
			return user.UserID
		}
		return 0
	}
	if user, ok := msg.PeerID.(*tg.PeerUser); ok {
		return user.UserID
	}
	return 0
}
