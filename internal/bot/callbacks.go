package bot

import (
	"context"
	"strings"

	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/tg"
)

// callback carries one inline-button press through its handler.
type callback struct {
	ctx     context.Context
	queryID int64
	userID  int64
	chatID  int64
	msgID   int
	peer    tg.InputPeerClass
	data    string
}

// onCallback routes an inline-button press to the flow that owns it.
func (b *Bot) onCallback(e tg.Entities, u *tg.UpdateBotCallbackQuery) {
	target, err := peer.EntitiesFromUpdate(e).ExtractPeer(u.Peer)
	if err != nil {
		b.log.Error("Could not resolve callback peer", "err", err)
		return
	}

	cb := &callback{
		ctx:     b.runCtx,
		queryID: u.QueryID,
		userID:  u.UserID,
		chatID:  chatID(u.Peer),
		msgID:   u.MsgID,
		peer:    target,
		data:    string(u.Data),
	}

	var handler func(*callback) error
	switch {
	case strings.HasPrefix(cb.data, "srch"):
		handler = b.onSearchCallback
	case strings.HasPrefix(cb.data, "clear:"), strings.HasPrefix(cb.data, "clrpg:"), cb.data == "clrnoop":
		handler = b.onClearCallback
	default:
		return
	}

	b.goHandle("callback "+cb.data, func() {
		if err := handler(cb); err != nil {
			b.log.Error("Callback failed", "data", cb.data, "err", err)
		}
	})
}
