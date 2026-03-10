package memlog

import (
	"strings"

	"github.com/k3s-io/kine/pkg/server"
)

func (m *MemLog) revToIndex(rev int64) int {
	idx := int(rev - 1)
	if idx < 0 {
		return -1
	}
	if idx >= len(m.entries) {
		return len(m.entries) - 1
	}
	return idx
}

func (m *MemLog) entryAt(rev int64) *entry {
	idx := int(rev - 1)
	if idx < 0 || idx >= len(m.entries) {
		return nil
	}
	return m.entries[idx]
}

func (m *MemLog) toEvent(e *entry, includeValue, includePrevValue bool) *server.Event {
	ev := &server.Event{
		Create: e.created,
		Delete: e.deleted,
		KV: &server.KeyValue{
			Key:            e.key,
			ModRevision:    e.id,
			CreateRevision: e.createRevision,
			Lease:          e.lease,
		},
	}
	if includeValue {
		ev.KV.Value = e.value
	}
	if e.created {
		ev.KV.CreateRevision = e.id
		ev.PrevKV = nil
	} else {
		ev.PrevKV = &server.KeyValue{
			ModRevision: e.prevRevision,
		}
		if includePrevValue {
			ev.PrevKV.Value = e.prevValue
		}
	}
	return ev
}

func matchesPattern(key, pattern string) bool {
	if strings.HasSuffix(pattern, "%") {
		return strings.HasPrefix(key, pattern[:len(pattern)-1])
	}
	return key == pattern
}

func filterByPrefix(events []*server.Event, checkPrefix bool, prefix string) []*server.Event {
	filtered := make([]*server.Event, 0, len(events))
	for _, ev := range events {
		if (checkPrefix && strings.HasPrefix(ev.KV.Key, prefix)) || ev.KV.Key == prefix {
			filtered = append(filtered, ev)
		}
	}
	return filtered
}
