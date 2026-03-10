package memlog

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

// Compact removes superseded and deleted entries up to the target revision.
func (m *MemLog) Compact(_ context.Context, revision int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.doCompact(revision)
	return m.currentRev, nil
}

func (m *MemLog) compactor() {
	t := time.NewTicker(m.compactInterval)
	defer t.Stop()

	targetRev := m.compactRev

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
		}

		m.mu.RLock()
		currentRev := m.currentRev
		m.mu.RUnlock()

		newTarget := currentRev - m.compactMinRetain
		if newTarget <= targetRev {
			continue
		}
		targetRev = newTarget

		m.mu.Lock()
		m.doCompact(targetRev)
		m.mu.Unlock()

		logrus.Debugf("MEMLOG COMPACT compacted to revision %d (current %d)", m.compactRev, currentRev)
	}
}

func (m *MemLog) doCompact(targetRev int64) {
	safe := m.currentRev - m.compactMinRetain
	if targetRev > safe {
		targetRev = safe
	}
	if targetRev <= m.compactRev || targetRev <= 0 {
		return
	}

	superseded := make(map[int64]struct{})
	bound := m.revToIndex(targetRev)
	for i := 0; i <= bound && i < len(m.entries); i++ {
		e := m.entries[i]
		if e == nil {
			continue
		}
		if e.key == "compact_rev_key" {
			continue
		}
		if e.prevRevision != 0 {
			superseded[e.prevRevision] = struct{}{}
		}
		if e.deleted {
			superseded[e.id] = struct{}{}
		}
	}

	for rev := range superseded {
		idx := int(rev - 1)
		if idx >= 0 && idx < len(m.entries) {
			m.entries[idx] = nil
		}
	}

	m.compactRev = targetRev
}
