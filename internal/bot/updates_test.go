package bot

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/gotd/td/tg"
)

type countingHandler struct{ calls int }

func (c *countingHandler) Handle(ctx context.Context, u tg.UpdatesClass) error {
	c.calls++
	return nil
}

// TestUpdateLoggerReportsDroppedForms pins the one risk of handing updates
// straight to the dispatcher: the short message forms carry no peer, so the
// dispatcher drops them. A bot should never be sent one, and if that is ever
// wrong the log has to say so -- otherwise the command just goes unanswered.
func TestUpdateLoggerReportsDroppedForms(t *testing.T) {
	cases := map[string]tg.UpdatesClass{
		"short message":      &tg.UpdateShortMessage{},
		"short chat message": &tg.UpdateShortChatMessage{},
		"too long":           &tg.UpdatesTooLong{},
	}

	for what, update := range cases {
		var logged bytes.Buffer
		next := &countingHandler{}
		handler := updateLogger{
			next: next,
			bot:  &Bot{log: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))},
		}

		if err := handler.Handle(context.Background(), update); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if !strings.Contains(logged.String(), "level=WARN") {
			t.Errorf("%s was passed over in silence: %q", what, logged.String())
		}
		// Still forwarded: the dispatcher decides what it can use.
		if next.calls != 1 {
			t.Errorf("%s: forwarded %d times, want 1", what, next.calls)
		}
	}
}

// TestUpdateLoggerStaysQuietForNormalUpdates: the ordinary form a bot receives
// carries its users and chats and must not be reported as a problem.
func TestUpdateLoggerStaysQuietForNormalUpdates(t *testing.T) {
	var logged bytes.Buffer
	next := &countingHandler{}
	handler := updateLogger{
		next: next,
		bot:  &Bot{log: slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelWarn}))},
	}

	if err := handler.Handle(context.Background(), &tg.Updates{}); err != nil {
		t.Fatal(err)
	}
	if logged.Len() != 0 {
		t.Errorf("an ordinary update logged: %q", logged.String())
	}
	if next.calls != 1 {
		t.Errorf("forwarded %d times, want 1", next.calls)
	}
}
