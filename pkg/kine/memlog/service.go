package memlog

import (
	"context"
	"sync"
	"time"

	"github.com/k3s-io/kine/pkg/broadcaster"
	"github.com/k3s-io/kine/pkg/server"
)

// entry mirrors a row in the kine SQL table.
type entry struct {
	id             int64
	key            string
	created        bool
	deleted        bool
	createRevision int64
	prevRevision   int64
	lease          int64
	value          []byte
	prevValue      []byte
}

// MemLog implements logstructured.Log backed entirely by in-memory data structures.
type MemLog struct {
	mu         sync.RWMutex
	entries    []*entry         // append-only log; entries[i].id == i+1 when non-nil
	latestRev  map[string]int64 // key -> revision of the latest entry for that key
	currentRev int64
	compactRev int64

	broadcaster broadcaster.Broadcaster
	watchCh     chan any // input channel for the broadcaster

	compactInterval  time.Duration
	compactBatchSize int64
	compactMinRetain int64
	ctx              context.Context
}

// New creates a MemLog with the given compaction parameters.
func New(compactInterval time.Duration, compactBatchSize, compactMinRetain int64) *MemLog {
	if compactBatchSize < 100 {
		compactBatchSize = 100
	}
	if compactMinRetain < 1000 {
		compactMinRetain = 1000
	}
	return &MemLog{
		entries:          make([]*entry, 0, 4096),
		latestRev:        make(map[string]int64),
		compactInterval:  compactInterval,
		compactBatchSize: compactBatchSize,
		compactMinRetain: compactMinRetain,
	}
}

// Start initialises the compact_rev_key marker (mirrors SQLLog.compactStart).
func (m *MemLog) Start(ctx context.Context) error {
	m.ctx = ctx
	_, err := m.Append(ctx, &server.Event{
		Create: true,
		KV: &server.KeyValue{
			Key:   "compact_rev_key",
			Value: []byte(""),
		},
	})
	return err
}

// CompactRevision returns the revision up to which old entries have been removed.
func (m *MemLog) CompactRevision(_ context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.compactRev, nil
}

// CurrentRevision returns the latest revision.
func (m *MemLog) CurrentRevision(_ context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentRev, nil
}
