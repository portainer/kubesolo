package memlog

import (
	"context"
	"sort"

	"github.com/k3s-io/kine/pkg/server"
)

// List returns events matching prefix/startKey constraints.
// When revision == 0, returns the current (latest) state of each key.
// When revision > 0, returns the state as of that revision.
func (m *MemLog) List(_ context.Context, prefix, startKey string, limit, revision int64, includeDeletes, keysOnly bool) (int64, []*server.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rev := m.currentRev
	if revision > 0 && revision > m.currentRev {
		return rev, nil, server.ErrFutureRev
	}
	if revision > 0 && revision < m.compactRev {
		return rev, nil, server.ErrCompacted
	}

	var events []*server.Event
	if revision == 0 {
		events = m.listCurrent(prefix, startKey, limit, includeDeletes, keysOnly)
	} else {
		events = m.listAtRevision(prefix, startKey, limit, revision, includeDeletes, keysOnly)
		rev = revision
	}

	return rev, events, nil
}

func (m *MemLog) listCurrent(prefix, startKey string, limit int64, includeDeletes, keysOnly bool) []*server.Event {
	type kv struct {
		key string
		e   *entry
	}
	var results []kv

	for key, rev := range m.latestRev {
		if !matchesPattern(key, prefix) {
			continue
		}
		if startKey != "" && key < startKey {
			continue
		}
		e := m.entryAt(rev)
		if e == nil {
			continue
		}
		if e.deleted && !includeDeletes {
			continue
		}
		results = append(results, kv{key: key, e: e})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].key < results[j].key
	})

	if limit > 0 && int64(len(results)) > limit {
		results = results[:limit]
	}

	events := make([]*server.Event, 0, len(results))
	for _, r := range results {
		events = append(events, m.toEvent(r.e, !keysOnly, false))
	}
	return events
}

func (m *MemLog) listAtRevision(prefix, startKey string, limit, revision int64, includeDeletes, keysOnly bool) []*server.Event {
	latest := make(map[string]*entry)
	bound := m.revToIndex(revision)
	for i := 0; i <= bound; i++ {
		e := m.entries[i]
		if e == nil {
			continue
		}
		if !matchesPattern(e.key, prefix) {
			continue
		}
		if startKey != "" && e.key < startKey {
			continue
		}
		latest[e.key] = e
	}

	type kv struct {
		key string
		e   *entry
	}
	var results []kv
	for key, e := range latest {
		if e.deleted && !includeDeletes {
			continue
		}
		results = append(results, kv{key: key, e: e})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].key < results[j].key
	})

	if limit > 0 && int64(len(results)) > limit {
		results = results[:limit]
	}

	events := make([]*server.Event, 0, len(results))
	for _, r := range results {
		events = append(events, m.toEvent(r.e, !keysOnly, false))
	}
	return events
}

// Count returns the count of keys matching prefix/startKey constraints.
func (m *MemLog) Count(_ context.Context, prefix, startKey string, revision int64) (int64, int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if revision == 0 {
		return m.currentRev, m.countCurrent(prefix, startKey), nil
	}
	return revision, m.countAtRevision(prefix, startKey, revision), nil
}

func (m *MemLog) countCurrent(prefix, startKey string) int64 {
	var count int64
	for key, rev := range m.latestRev {
		if !matchesPattern(key, prefix) {
			continue
		}
		if startKey != "" && key < startKey {
			continue
		}
		e := m.entryAt(rev)
		if e == nil || e.deleted {
			continue
		}
		count++
	}
	return count
}

func (m *MemLog) countAtRevision(prefix, startKey string, revision int64) int64 {
	latest := make(map[string]*entry)
	bound := m.revToIndex(revision)
	for i := 0; i <= bound; i++ {
		e := m.entries[i]
		if e == nil {
			continue
		}
		if !matchesPattern(e.key, prefix) {
			continue
		}
		if startKey != "" && e.key < startKey {
			continue
		}
		latest[e.key] = e
	}
	var count int64
	for _, e := range latest {
		if !e.deleted {
			count++
		}
	}
	return count
}

// After returns all entries with id > revision matching prefix, ordered by id ASC.
func (m *MemLog) After(_ context.Context, prefix string, revision, limit int64) (int64, []*server.Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if revision > 0 && revision < m.compactRev {
		return m.currentRev, nil, server.ErrCompacted
	}

	var events []*server.Event
	start := m.revToIndex(revision) + 1
	if start < 0 {
		start = 0
	}
	for i := start; i < len(m.entries); i++ {
		e := m.entries[i]
		if e == nil {
			continue
		}
		if !matchesPattern(e.key, prefix) {
			continue
		}
		events = append(events, m.toEvent(e, true, true))
		if limit > 0 && int64(len(events)) >= limit {
			break
		}
	}

	return m.currentRev, events, nil
}
