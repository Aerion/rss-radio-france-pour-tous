package feedcache

import (
	"context"
	"sync"
	"time"
)

type memStoreKey struct {
	showID string
	page   int
}

// MemStore is an in-memory store, for tests that need a working Cache
// without a real database. Exported (rather than kept in a _test.go file)
// because Cache is constructed directly by other packages' tests - notably
// internal/httpapi's - which can't reach an unexported test helper across
// the package boundary. Not used by the running server, which always uses
// Store (Postgres-backed).
type MemStore struct {
	mu   sync.Mutex
	rows map[memStoreKey]Row
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{rows: map[memStoreKey]Row{}}
}

// Get returns the stored row for showID's page, or ok=false if there is none.
func (m *MemStore) Get(_ context.Context, showID string, page int) (Row, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[memStoreKey{showID, page}]
	return r, ok, nil
}

// Set stores r under showID's page.
func (m *MemStore) Set(_ context.Context, showID string, page int, r Row) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[memStoreKey{showID, page}] = r
	return nil
}

// Delete removes showID's page row, if any.
func (m *MemStore) Delete(_ context.Context, showID string, page int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, memStoreKey{showID, page})
	return nil
}

// DeleteStalePage0 removes every page-0 row cached at or before olderThan.
func (m *MemStore) DeleteStalePage0(_ context.Context, olderThan time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for key, r := range m.rows {
		if key.page == 0 && !r.CachedAt.After(olderThan) {
			delete(m.rows, key)
			removed++
		}
	}
	return removed, nil
}

// Count returns the total number of rows currently stored.
func (m *MemStore) Count(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.rows)), nil
}
