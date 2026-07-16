package shows

import (
	"context"
	"testing"
	"time"
)

// newTestRegistry builds a Registry with no worker goroutines (workers=0),
// so tests can inspect queued updates directly from the channel without a
// real database.
func newTestRegistry(t *testing.T, bufferSize int) *Registry {
	t.Helper()
	return NewRegistry(nil, bufferSize, 0)
}

func TestRecord_QueuesUpdate(t *testing.T) {
	r := newTestRegistry(t, 1)
	r.Record(context.Background(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", "Affaires sensibles")

	select {
	case u := <-r.updates:
		if u.showID != "0b91efaf-26e6-11e4-907f-782bcb6744eb" {
			t.Errorf("showID = %q", u.showID)
		}
		if u.title != "Affaires sensibles" {
			t.Errorf("title = %q", u.title)
		}
	default:
		t.Fatal("expected an update to be queued")
	}
}

func TestRecord_NoopWhenShowIDOrTitleEmpty(t *testing.T) {
	r := newTestRegistry(t, 1)
	r.Record(context.Background(), "", "Affaires sensibles")
	r.Record(context.Background(), "0b91efaf-26e6-11e4-907f-782bcb6744eb", "")

	select {
	case u := <-r.updates:
		t.Fatalf("expected no update to be queued, got %+v", u)
	default:
	}
}

func TestRecord_DropsUpdateWhenBufferFull(t *testing.T) {
	r := newTestRegistry(t, 1)

	// Fill the buffer (size 1), then send a second update that should be
	// dropped rather than blocking.
	r.Record(context.Background(), "show-1", "Show One")

	done := make(chan struct{})
	go func() {
		r.Record(context.Background(), "show-2", "Show Two")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Record blocked instead of dropping the update when the buffer was full")
	}

	if len(r.updates) != 1 {
		t.Errorf("len(updates) = %d, want 1 (buffer full, second update dropped)", len(r.updates))
	}
}
