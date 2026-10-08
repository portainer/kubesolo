package executor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/k3s-io/kine/pkg/drivers"
	"github.com/k3s-io/kine/pkg/drivers/sqlite"
	_ "github.com/mattn/go-sqlite3" // registers the sqlite3 driver
	"golang.org/x/sys/unix"
)

// SnapshotDatastore writes a consistent copy of the live datastore at src to
// dst while kine keeps running.
//
// VACUUM INTO reads the database inside one transaction, so the copy is a
// single point in time even with kine writing, and it folds the WAL in: the
// result is one self-contained file with no -wal or -shm to keep beside it.
// Copying state.db alone would lose every transaction still in the WAL.
func SnapshotDatastore(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("datastore: %w", err)
	}
	_ = os.Remove(dst) // VACUUM INTO refuses an existing file

	db, err := sql.Open("sqlite3", "file:"+src+"?_busy_timeout=30000")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`VACUUM INTO ?`, dst); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("snapshot the datastore: %w", err)
	}
	return syncFile(dst)
}

// QuickCheck runs SQLite's quick_check on a database file.
func QuickCheck(path string) error {
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`PRAGMA quick_check`)
	if err != nil {
		return fmt.Errorf("quick_check %s: %w", path, err)
	}
	defer func() { _ = rows.Close() }()

	var problems []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s failed its integrity check: %s", path, strings.Join(problems, "; "))
	}
	return nil
}

// CheckDatastore is the datastore dry-run, run by the *new* binary against a
// copy of the datastore: it opens the copy exactly as kine will, which applies
// kine's schema setup and data migration, then checks the result is intact and
// readable.
//
// It catches the one failure that makes a rollback ugly — a binary that cannot
// open, or corrupts, the store it is about to be started on — while the real
// datastore is untouched and the old binary is still running.
func CheckDatastore(path string) error {
	// kine retries a database it cannot open for a minute before giving up.
	// SQLite's own check answers in milliseconds, so a file that is not an
	// intact database never reaches it.
	if err := QuickCheck(path); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	_, dialect, err := sqlite.NewVariant(ctx, &wg, "sqlite3", &drivers.Config{
		DataSourceName: path + "?" + sqlite.DefaultParams,
	})
	if err != nil {
		return fmt.Errorf("kine could not open the datastore: %w", err)
	}

	var rows int64
	if err := dialect.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM kine`).Scan(&rows); err != nil {
		return fmt.Errorf("read the migrated datastore: %w", err)
	}
	var rev int64
	err = dialect.DB.QueryRowContext(ctx, `SELECT MAX(id) FROM kine`).Scan(&rev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read the datastore revision: %w", err)
	}
	if _, err := dialect.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("checkpoint the migrated datastore: %w", err)
	}
	cancel()
	wg.Wait()
	_ = dialect.DB.Close()

	if err := QuickCheck(path); err != nil {
		return err
	}
	fmt.Printf("datastore ok: %d rows, revision %d\n", rows, rev)
	return nil
}

// CloneFile copies src to dst, sharing blocks when the filesystem supports it
// (FICLONE: btrfs, XFS with reflink, bcachefs, overlayfs on those). A clone is
// instant and takes no space until either file changes. Elsewhere it is an
// ordinary copy. dst is synced either way.
func CloneFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
	}
	if err := out.Chmod(mode); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
