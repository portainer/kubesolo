package memlog

import (
	"context"
	"strings"

	"github.com/k3s-io/kine/pkg/server"
)

// Watch subscribes to new events matching prefix.
func (m *MemLog) Watch(ctx context.Context, prefix string) <-chan []*server.Event {
	res := make(chan []*server.Event, 100)
	values, err := m.broadcaster.Subscribe(ctx, m.startWatch)
	if err != nil {
		close(res)
		return res
	}

	checkPrefix := strings.HasSuffix(prefix, "/")

	go func() {
		defer close(res)
		for i := range values {
			events, ok := i.([]*server.Event)
			if !ok {
				continue
			}
			filtered := filterByPrefix(events, checkPrefix, prefix)
			if len(filtered) > 0 {
				res <- filtered
			}
		}
	}()

	return res
}

func (m *MemLog) startWatch() (chan any, error) {
	c := make(chan any, 1024)
	m.mu.Lock()
	m.watchCh = c
	m.mu.Unlock()

	if m.compactInterval > 0 {
		go m.compactor()
	}
	return c, nil
}

// Append inserts a new entry and broadcasts it to watchers.
func (m *MemLog) Append(_ context.Context, event *server.Event) (int64, error) {
	e := *event
	if e.KV == nil {
		e.KV = &server.KeyValue{}
	}
	if e.PrevKV == nil {
		e.PrevKV = &server.KeyValue{}
	}

	m.mu.Lock()
	m.currentRev++
	rev := m.currentRev

	ent := &entry{
		id:             rev,
		key:            e.KV.Key,
		created:        e.Create,
		deleted:        e.Delete,
		createRevision: e.KV.CreateRevision,
		prevRevision:   e.PrevKV.ModRevision,
		lease:          e.KV.Lease,
		value:          e.KV.Value,
		prevValue:      e.PrevKV.Value,
	}
	m.entries = append(m.entries, ent)
	m.latestRev[ent.key] = rev
	watchCh := m.watchCh
	m.mu.Unlock()

	if watchCh != nil {
		broadcastEvent := m.toEvent(ent, true, true)
		select {
		case watchCh <- []*server.Event{broadcastEvent}:
		default:
		}
	}

	return rev, nil
}

// DbSize returns an estimate of the in-memory size in bytes.
func (m *MemLog) DbSize(_ context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var size int64
	for _, e := range m.entries {
		if e == nil {
			continue
		}
		size += int64(len(e.key)) + int64(len(e.value)) + int64(len(e.prevValue)) + 64
	}
	return size, nil
}
