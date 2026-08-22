package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tgerr"
)

// fakeInvoker answers with the given errors in order, then with nil.
type fakeInvoker struct {
	errs  []error
	calls int
}

func (f *fakeInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	f.calls++
	if f.calls <= len(f.errs) {
		return f.errs[f.calls-1]
	}
	return nil
}

func newWaiter(t *testing.T, inv *fakeInvoker) func(context.Context) error {
	t.Helper()
	handle := floodWaiter(slog.New(slog.NewTextHandler(io.Discard, nil))).Handle(inv)
	return func(ctx context.Context) error { return handle(ctx, nil, nil) }
}

// TestFloodWaiterRetries covers the case the middleware exists for: Telegram
// throttles one call, and the command answers a moment later instead of
// failing outright.
func TestFloodWaiterRetries(t *testing.T) {
	inv := &fakeInvoker{errs: []error{tgerr.New(420, "FLOOD_WAIT_0")}}

	start := time.Now()
	if err := newWaiter(t, inv)(context.Background()); err != nil {
		t.Fatalf("invoke = %v, want the retry to succeed", err)
	}
	if inv.calls != 2 {
		t.Errorf("calls = %d, want the request sent twice", inv.calls)
	}
	// Telegram's figure is a floor, so the wait is its value plus a second.
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("retried after %v, want at least the second added to the wait", elapsed)
	}
}

// TestFloodWaiterGivesUp: a wait longer than anyone should sit through is
// returned to the caller rather than holding the command open.
func TestFloodWaiterGivesUp(t *testing.T) {
	inv := &fakeInvoker{errs: []error{tgerr.New(420, "FLOOD_WAIT_3600")}}

	err := newWaiter(t, inv)(context.Background())
	if _, ok := tgerr.AsFloodWait(err); !ok {
		t.Errorf("invoke = %v, want the flood wait reported", err)
	}
	if inv.calls != 1 {
		t.Errorf("calls = %d, want no retry", inv.calls)
	}
}

// TestFloodWaiterPassesOtherErrors: only flood waits are retried.
func TestFloodWaiterPassesOtherErrors(t *testing.T) {
	want := errors.New("nope")
	inv := &fakeInvoker{errs: []error{want}}

	if err := newWaiter(t, inv)(context.Background()); !errors.Is(err, want) {
		t.Errorf("invoke = %v, want %v", err, want)
	}
	if inv.calls != 1 {
		t.Errorf("calls = %d, want no retry", inv.calls)
	}
}

// TestFloodWaiterHonoursCancellation: shutdown does not wait out a flood.
func TestFloodWaiterHonoursCancellation(t *testing.T) {
	inv := &fakeInvoker{errs: []error{tgerr.New(420, "FLOOD_WAIT_10")}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newWaiter(t, inv)(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("invoke = %v, want context.Canceled", err)
	}
}
