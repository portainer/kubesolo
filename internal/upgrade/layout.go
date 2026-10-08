// Package upgrade holds the parts of an in-place KubeSolo upgrade that both the
// kubesolo binary and kubesoloctl need: where the upgrade keeps its files, the
// record of what it is doing and what it last did, the lock that makes it the
// only writer, and fetching and checking a release before anything is touched.
//
// It is pure Go. Work that needs SQLite or a Kubernetes client — the backup,
// the datastore dry-run, the health gate — lives in internal/upgrade/executor,
// which only the kubesolo binary links. kubesoloctl is built without cgo.
//
// The order of an upgrade is the point of the design: everything that can fail
// does so while KubeSolo is still running and untouched.
//
//	fetch → checksum → arch/libc → version → disk space → backup → dry-run
//	                                                                  ↓
//	                                        stop → replace → start → verify
//
// An abort anywhere before the arrow leaves a host that never noticed.
package upgrade

import (
	"path/filepath"

	"github.com/portainer/kubesolo/types"
)

// BinaryPath is where every installer puts the kubesolo binary.
const BinaryPath = "/usr/local/bin/kubesolo"

// MaxBootAttempts is how many starts of a new binary the boot guard allows
// before it restores the previous one. Each start that dies before KubeSolo
// reports itself healthy uses one.
const MaxBootAttempts = 3

// Layout names the files an upgrade reads and writes. Everything except the
// binary and the configuration file lives under <data>/upgrade, on the same
// filesystem as the datastore, so restoring is a rename or a reflink rather
// than a copy across devices.
type Layout struct {
	// DataDir is KubeSolo's data directory, normally /var/lib/kubesolo.
	DataDir string

	// Binary is the installed kubesolo binary.
	Binary string

	// ConfigFile is the configuration file, normally /etc/kubesolo/config.yaml.
	// It may not exist on a flag-based install.
	ConfigFile string
}

// NewLayout returns the layout for a data directory and configuration file,
// with the standard binary path.
func NewLayout(dataDir, configFile string) Layout {
	if dataDir == "" {
		dataDir = types.DefaultBasePath
	}
	if configFile == "" {
		configFile = types.DefaultConfigFile
	}
	return Layout{DataDir: dataDir, Binary: BinaryPath, ConfigFile: configFile}
}

// Dir is the upgrade's own directory.
func (l Layout) Dir() string { return filepath.Join(l.DataDir, "upgrade") }

// StateFile records the upgrade in flight and the outcome of the last one.
func (l Layout) StateFile() string { return filepath.Join(l.Dir(), "state.json") }

// LockFile is held for the whole of an upgrade or rollback.
func (l Layout) LockFile() string { return filepath.Join(l.Dir(), "lock") }

// StagingDir holds downloaded archives and extracted binaries until a binary is
// moved into place, one subdirectory per run.
func (l Layout) StagingDir() string { return filepath.Join(l.Dir(), "staging") }

// RunStagingDir is a run's own staging directory.
func (l Layout) RunStagingDir(id string) string { return filepath.Join(l.StagingDir(), id) }

// BackupDir is the single retained backup: the previous binary, the datastore
// and the configuration file as they were before the last upgrade.
func (l Layout) BackupDir() string { return filepath.Join(l.Dir(), "backup") }

// PreviousBackupDir holds the backup BackupDir replaced, while the upgrade that
// replaced it is unproven. A failed upgrade puts it back; see SettleBackups.
func (l Layout) PreviousBackupDir() string { return filepath.Join(l.Dir(), "backup.prev") }

// BackupManifest describes the backup in BackupDir.
func (l Layout) BackupManifest() string { return filepath.Join(l.BackupDir(), "manifest.json") }

// BackupBinary is the previous kubesolo binary.
func (l Layout) BackupBinary() string { return filepath.Join(l.BackupDir(), "kubesolo") }

// BackupDatastore is the datastore as it was before the upgrade.
func (l Layout) BackupDatastore() string {
	return filepath.Join(l.BackupDir(), types.DefaultKineDBFile)
}

// BackupConfigFile is the configuration file as it was before the upgrade.
func (l Layout) BackupConfigFile() string { return filepath.Join(l.BackupDir(), "config.yaml") }

// RunLog is a run's progress as plain text, one line per message, ending with
// a line "finished: <result>". It is for followers that cannot read JSON, such
// as install.sh.
func (l Layout) RunLog(id string) string { return filepath.Join(l.Dir(), "run-"+id+".log") }

// BackupServiceDefinition is the service definition as it was before the upgrade.
func (l Layout) BackupServiceDefinition() string {
	return filepath.Join(l.BackupDir(), "service-definition")
}

// GuardScript is the boot guard every init system runs before KubeSolo.
func (l Layout) GuardScript() string { return filepath.Join(l.Dir(), "guard.sh") }

// PendingFile exists while a new binary has been started but not yet proven
// healthy. It holds the version being verified. The boot guard reads it.
func (l Layout) PendingFile() string { return filepath.Join(l.Dir(), "pending") }

// AttemptsFile counts the starts of a pending binary.
func (l Layout) AttemptsFile() string { return filepath.Join(l.Dir(), "attempts") }

// GuardLog is appended to by the boot guard when it restores the previous
// binary, so that the next KubeSolo can report what happened.
func (l Layout) GuardLog() string { return filepath.Join(l.Dir(), "guard.log") }

// DatastoreDir is kine's database directory.
func (l Layout) DatastoreDir() string {
	return filepath.Join(l.DataDir, types.DefaultKineDir, "db")
}

// Datastore is kine's SQLite database. The -wal and -shm files sit beside it.
func (l Layout) Datastore() string {
	return filepath.Join(l.DatastoreDir(), types.DefaultKineDBFile)
}

// AdminKubeconfig is the admin kubeconfig KubeSolo writes into its PKI.
func (l Layout) AdminKubeconfig() string {
	return filepath.Join(l.DataDir, types.DefaultPKIDir, "admin", "admin.kubeconfig")
}
