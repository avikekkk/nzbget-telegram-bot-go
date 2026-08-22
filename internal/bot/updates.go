package bot

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// updateLogger passes updates to the dispatcher and reports the forms the
// dispatcher throws away.
//
// tg.UpdateDispatcher handles updates.Updates, updates.UpdatesCombined and
// updateShort, and silently returns nil for the "short" message forms, which
// carry no users or chats. Telegram sends those to clients that already know
// the peer from their dialog cache; a bot has no such cache and is sent the
// full form. That is an expectation, not a guarantee, so a dropped update is
// logged rather than passed over in silence: it would otherwise present as a
// command that was simply ignored, with nothing anywhere to say why.
type updateLogger struct {
	next telegram.UpdateHandler
	bot  *Bot
}

func (u updateLogger) Handle(ctx context.Context, updates tg.UpdatesClass) error {
	switch updates.(type) {
	case *tg.UpdateShortMessage, *tg.UpdateShortChatMessage:
		u.bot.log.Warn("Dropped a short-form update",
			"type", fmt.Sprintf("%T", updates),
			"hint", "the dispatcher cannot resolve its peer; report this if a command was ignored")
	case *tg.UpdatesTooLong:
		// Only a user account can recover these, by fetching the difference.
		// A bot is pushed its updates and has no state to catch up from.
		u.bot.log.Warn("Telegram reported too many updates to send")
	}
	return u.next.Handle(ctx, updates)
}
