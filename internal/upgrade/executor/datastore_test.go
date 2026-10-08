package executor

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k3s-io/kine/pkg/drivers"
	"github.com/k3s-io/kine/pkg/drivers/sqlite"
)

// newKineDB creates a datastore the way kine does and returns an open handle
// to it for writing more rows.
func newKineDB(t *testing.T, path string, rows int) *sql.DB {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	_, dialect, err := sqlite.NewVariant(ctx, &wg, "sqlite3", &drivers.Config{DataSourceName: path + "?" + sqlite.DefaultParams})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); wg.Wait(); _ = dialect.DB.Close() })
	for i := 0; i < rows; i++ {
		insertRow(t, dialect.DB, i)
	}
	return dialect.DB
}

func insertRow(t *testing.T, db *sql.DB, i int) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO kine(name, created, deleted, create_revision, prev_revision, lease, value, old_value) VALUES (?, 1, 0, 0, 0, 0, ?, ?)`,
		"/registry/configmaps/default/cm-"+strings.Repeat("x", i%7)+string(rune('a'+i%26))+time.Now().Format("150405.000000000"), []byte(strings.Repeat("v", 512)), []byte{})
	if err != nil {
		t.Fatal(err)
	}
}

func countRows(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM kine`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The snapshot is taken while kine writes, as it is during an upgrade. It must
// be a complete, consistent database on its own, with the WAL folded in.
func TestSnapshotDatastoreWhileWriting(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "state.db")
	db := newKineDB(t, live, 500)

	var written atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := db.Exec(`INSERT INTO kine(name, created, deleted, create_revision, prev_revision, lease, value, old_value) VALUES (?, 1, 0, 0, 0, 0, ?, ?)`,
				"/registry/live/"+time.Now().Format("150405.000000000"), []byte("v"), []byte{}); err == nil {
				written.Add(1)
			}
		}
	}()
	time.Sleep(100 * time.Millisecond)

	snap := filepath.Join(dir, "backup", "state.db")
	if err := os.MkdirAll(filepath.Dir(snap), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SnapshotDatastore(live, snap); err != nil {
		t.Fatal(err)
	}
	close(stop)
	<-done

	if err := QuickCheck(snap); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(snap + suffix); !os.IsNotExist(err) {
			t.Errorf("snapshot has a %s file; it must be self-contained", suffix)
		}
	}
	got := countRows(t, snap)
	if got < 500 {
		t.Errorf("snapshot has %d rows, fewer than the 500 written before it", got)
	}
	t.Logf("snapshot holds %d rows; %d were written concurrently", got, written.Load())

	// Taking it again over an existing file works.
	if err := SnapshotDatastore(live, snap); err != nil {
		t.Errorf("second snapshot: %v", err)
	}
}

func TestSnapshotOfMissingDatastore(t *testing.T) {
	if err := SnapshotDatastore(filepath.Join(t.TempDir(), "none.db"), filepath.Join(t.TempDir(), "s.db")); err == nil {
		t.Error("snapshot of a missing datastore succeeded")
	}
}

// The dry-run opens the copy as kine does and leaves it intact and readable.
func TestCheckDatastore(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "state.db")
	newKineDB(t, live, 50)
	snap := filepath.Join(dir, "snap.db")
	if err := SnapshotDatastore(live, snap); err != nil {
		t.Fatal(err)
	}
	if err := CheckDatastore(snap); err != nil {
		t.Fatalf("dry-run on a good datastore: %v", err)
	}
	if n := countRows(t, snap); n != 50 {
		t.Errorf("dry-run changed the row count to %d", n)
	}
}

// A datastore that is not a database, or is damaged, fails the dry-run before
// anything is stopped.
func TestCheckDatastoreRejectsDamage(t *testing.T) {
	dir := t.TempDir()

	junk := filepath.Join(dir, "junk.db")
	if err := os.WriteFile(junk, []byte(strings.Repeat("not sqlite ", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CheckDatastore(junk); err == nil {
		t.Error("dry-run accepted a file that is not a database")
	}

	live := filepath.Join(dir, "state.db")
	newKineDB(t, live, 2000)
	snap := filepath.Join(dir, "snap.db")
	if err := SnapshotDatastore(live, snap); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(snap, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := f.Stat()
	// Overwrite pages in the middle of the file, past the header.
	if _, err := f.WriteAt([]byte(strings.Repeat("\xff", 8192)), fi.Size()/2); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := QuickCheck(snap); err == nil {
		t.Error("quick_check passed a damaged database")
	}
}

func TestCloneFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte(strings.Repeat("data", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(dst, []byte("longer old contents that must be truncated away entirely, really"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := CloneFile(src, dst, 0o755); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(src)
	b, _ := os.ReadFile(dst)
	if string(a) != string(b) {
		t.Error("clone differs from its source")
	}
	if fi, _ := os.Stat(dst); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v", fi.Mode())
	}
}
