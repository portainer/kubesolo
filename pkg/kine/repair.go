package kine

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog/log"
)

// repairWALIfCorrupt runs a quick integrity check against the kine SQLite
// database. If the check fails (or the DB cannot be opened at all), the WAL
// artefacts — state.db-wal and state.db-shm — are removed so that SQLite
// falls back to the last cleanly checkpointed state on the next open.
//
// This recovers the common power-loss scenario where unsynced WAL frames
// leave the database in an unreadable or internally inconsistent state,
// causing the apiserver identity lease precondition check to fail and
// KubeSolo to abort on every subsequent boot.
func (s *service) repairWALIfCorrupt() {
	dbPath := filepath.Join(s.databaseDir, "state.db")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Warn().Str("component", "kine").Msgf("cannot open SQLite DB for integrity check, removing WAL artefacts: %v", err)
		removeWALArtefacts(dbPath)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		log.Warn().Str("component", "kine").Msgf("SQLite quick_check query failed, removing WAL artefacts: %v", err)
		removeWALArtefacts(dbPath)
		return
	}
	defer rows.Close()

	if rows.Next() {
		var result string
		if rows.Scan(&result) == nil && result == "ok" {
			log.Debug().Str("component", "kine").Msg("SQLite integrity check passed")
			return
		}
	}

	log.Warn().Str("component", "kine").Msg("SQLite integrity check failed, removing WAL artefacts to recover from unclean shutdown")
	removeWALArtefacts(dbPath)
}

func removeWALArtefacts(dbPath string) {
	for _, suffix := range []string{"-wal", "-shm"} {
		path := dbPath + suffix
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Warn().Str("component", "kine").Msgf("failed to remove %s: %v", path, err)
		} else if err == nil {
			log.Info().Str("component", "kine").Msgf("removed %s", path)
		}
	}
}
