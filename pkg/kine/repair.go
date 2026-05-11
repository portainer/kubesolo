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
// database. If the check fails (or the DB cannot be opened at all) and
// --db-wal-repair is enabled, the WAL artefacts (state.db-wal, state.db-shm)
// are removed so that SQLite falls back to the last cleanly checkpointed state.
// Without the flag, a fatal log is emitted with instructions for manual recovery
// so that operators are never silently left in a broken boot loop.
func (s *service) repairWALIfCorrupt() {
	dbPath := filepath.Join(s.databaseDir, "state.db")

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		s.handleCorruption(dbPath, "cannot open SQLite DB for integrity check: %v", err)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		s.handleCorruption(dbPath, "SQLite quick_check query failed: %v", err)
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

	s.handleCorruption(dbPath, "SQLite integrity check failed")
}

func (s *service) handleCorruption(dbPath string, format string, args ...any) {
	if s.dbWALRepair {
		log.Warn().Str("component", "kine").Msgf(format+", removing WAL artefacts to recover from unclean shutdown", args...)
		removeWALArtefacts(dbPath)
		return
	}

	log.Fatal().Str("component", "kine").Msgf(
		format+". The SQLite WAL artefacts may be corrupt after an unclean shutdown. "+
			"Remove %s-wal and %s-shm manually, or restart with --db-wal-repair to remove them automatically.",
		append(args, dbPath, dbPath)...,
	)
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
