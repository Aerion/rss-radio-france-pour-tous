package analytics

import (
	"context"
	"sync"
)

type contextKey struct{}

// eventFields accumulates the optional fields a handler can attach to the
// in-flight request's analytics event (see WithShow) - written to by the
// handler, read back by Writer.Wrap after the handler returns.
type eventFields struct {
	mu        sync.Mutex
	showID    string
	showTitle string
}

func newContext(ctx context.Context) (context.Context, *eventFields) {
	f := &eventFields{}
	return context.WithValue(ctx, contextKey{}, f), f
}

// WithShow records which show a request was for, so the analytics event
// Writer.Wrap eventually inserts includes it. Safe to call from a handler;
// a no-op if called outside a request wrapped by Writer.Wrap (e.g. in
// tests that don't go through the middleware).
func WithShow(ctx context.Context, showID, showTitle string) {
	f, ok := ctx.Value(contextKey{}).(*eventFields)
	if !ok {
		return
	}
	f.mu.Lock()
	f.showID = showID
	f.showTitle = showTitle
	f.mu.Unlock()
}

func (f *eventFields) snapshot() (showID, showTitle string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.showID, f.showTitle
}
