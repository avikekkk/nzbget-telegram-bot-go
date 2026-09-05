package bot

import (
	"context"
	"fmt"

	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/telegram/message/html"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/message/unpack"
	"github.com/gotd/td/tg"
)

// Ctx carries one command invocation through its handler.
type Ctx struct {
	ctx  context.Context
	bot  *Bot
	ents tg.Entities
	upd  message.AnswerableMessageUpdate
	msg  *tg.Message

	chatID  int64
	userID  int64
	command string
	args    string
}

// Peer resolves the chat this command came from.
func (c *Ctx) Peer() (tg.InputPeerClass, error) {
	return peer.EntitiesFromUpdate(c.ents).ExtractPeer(c.msg.PeerID)
}

// FirstName is the sender's first name, for mentions.
func (c *Ctx) FirstName() string {
	if user, ok := c.ents.Users[c.userID]; ok {
		return user.FirstName
	}
	return ""
}

// Reply sends an HTML reply quoting the command message.
func (c *Ctx) Reply(text string) (*tg.Message, error) {
	ctx, cancel := c.bot.reporting(c.ctx)
	defer cancel()
	return unpack.Message(c.bot.sender.Reply(c.ents, c.upd).
		StyledText(ctx, html.String(nil, text)))
}

// ReplyMarkup sends an HTML reply with an inline keyboard.
func (c *Ctx) ReplyMarkup(text string, markup tg.ReplyMarkupClass) (*tg.Message, error) {
	ctx, cancel := c.bot.reporting(c.ctx)
	defer cancel()
	return unpack.Message(c.bot.sender.Reply(c.ents, c.upd).
		Markup(markup).
		StyledText(ctx, html.String(nil, text)))
}

// Edit replaces the text of a message the bot sent in this chat.
func (c *Ctx) Edit(msgID int, text string) error {
	target, err := c.Peer()
	if err != nil {
		return err
	}
	return c.bot.edit(c.ctx, target, msgID, text)
}

// Delete removes a message the bot sent in this chat.
func (c *Ctx) Delete(msgID int) error {
	target, err := c.Peer()
	if err != nil {
		return err
	}
	return c.bot.delete(c.ctx, target, msgID)
}

func (b *Bot) edit(ctx context.Context, target tg.InputPeerClass, msgID int, text string) error {
	ctx, cancel := b.reporting(ctx)
	defer cancel()
	_, err := b.sender.To(target).Edit(msgID).StyledText(ctx, html.String(nil, text))
	return err
}

func (b *Bot) editMarkup(ctx context.Context, target tg.InputPeerClass, msgID int, text string, markup tg.ReplyMarkupClass) error {
	ctx, cancel := b.reporting(ctx)
	defer cancel()
	_, err := b.sender.To(target).Markup(markup).Edit(msgID).StyledText(ctx, html.String(nil, text))
	return err
}

func (b *Bot) delete(ctx context.Context, target tg.InputPeerClass, msgID int) error {
	ctx, cancel := b.reporting(ctx)
	defer cancel()
	_, err := b.sender.To(target).Revoke().Messages(ctx, msgID)
	return err
}

// answer responds to a callback query, optionally as a popup.
func (b *Bot) answer(ctx context.Context, queryID int64, text string, alert bool) error {
	req := &tg.MessagesSetBotCallbackAnswerRequest{
		QueryID: queryID,
		Alert:   alert,
	}
	if text != "" {
		req.SetMessage(text)
	}
	ctx, cancel := b.reporting(ctx)
	defer cancel()
	_, err := b.api.MessagesSetBotCallbackAnswer(ctx, req)
	return err
}

// mention renders an inline link to a user profile.
func mention(userID int64, firstName string) string {
	return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, userID, escape(firstName))
}
