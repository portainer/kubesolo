package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/portainer/kubesolo/types"
	"sigs.k8s.io/yaml"
)

const (
	// configFileMode keeps the file readable only by root. It carries
	// portainer.edgeKey, which is a credential.
	configFileMode = 0o600

	// configDirMode is the mode /etc/kubesolo is created with when absent. The
	// directory is traversable; the file inside it is not readable.
	configDirMode = 0o755

	// backupSuffix names the copy of the previous file kept beside it. Writing
	// re-serialises the document, so comments and key order in a hand-edited
	// file are lost; the backup is what makes that recoverable.
	backupSuffix = ".bak"
)

// Read parses a config file onto cfg, leaving keys the file does not mention at
// whatever cfg already holds. Callers pass Defaults(), so a file that sets one
// setting does not reset the rest.
//
// A missing file is not an error: it reports found=false and leaves cfg alone.
// That is the state of every install made before the config file existed.
func Read(path string, cfg *types.Config) (found bool, warnings []Warning, err error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("read %s: %w", path, err)
	}

	warnings, err = unmarshal(raw, path, cfg)
	return true, warnings, err
}

// unmarshal applies raw onto cfg, reporting unknown keys as warnings rather
// than errors so a config written by a newer KubeSolo still boots on an older
// binary. Malformed YAML and wrongly typed values remain errors.
func unmarshal(raw []byte, source string, cfg *types.Config) ([]Warning, error) {
	var warnings []Warning

	// The schema version is read separately: cfg already carries the current
	// version from Defaults(), so after unmarshalling there is no way to tell a
	// file that omitted it from one that got it right.
	var meta struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	switch meta.APIVersion {
	case types.ConfigAPIVersion:
	case "":
		warnings = append(warnings, Warning{
			Message: fmt.Sprintf("%s does not declare an apiVersion; assuming %s", source, types.ConfigAPIVersion),
		})
	default:
		return nil, fmt.Errorf("%s declares apiVersion %q, which this version of KubeSolo does not understand (expected %s)",
			source, meta.APIVersion, types.ConfigAPIVersion)
	}

	// A wrong kind is a warning rather than an error, unlike a wrong apiVersion.
	// An apiVersion KubeSolo does not know means the schema cannot be
	// interpreted; a wrong kind alongside a known apiVersion is a copy-and-paste
	// artefact from another tool, where the schema is not in doubt. Saying so
	// beats accepting it in silence.
	//
	// An absent kind is not reported: Write fills it in, and a file that declares
	// neither field has already been warned about above.
	if meta.Kind != "" && meta.Kind != types.ConfigKind {
		warnings = append(warnings, Warning{
			Message: fmt.Sprintf("%s declares kind %q; KubeSolo only has %s, and read the file as one",
				source, meta.Kind, types.ConfigKind),
		})
	}

	// Strict decoding rejects unknown and duplicated keys; lenient decoding does
	// not. Running both distinguishes the two failure modes without matching on
	// error strings: if only the strict pass fails, the file carries keys this
	// binary does not know, which is a warning. If both fail, the file is
	// genuinely malformed.
	strictErr := yaml.UnmarshalStrict(raw, &types.Config{})
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", source, err)
	}
	if strictErr != nil {
		warnings = append(warnings, Warning{
			Message: fmt.Sprintf("%s contains settings this version of KubeSolo does not recognise, which were ignored: %v", source, strictErr),
		})
	}

	return warnings, nil
}

// Write saves cfg, replacing any existing file atomically.
//
// The new document is written to a temporary file in the same directory, then
// renamed over the target. At no point does the path hold a partial document: a
// reader sees either the whole previous file or the whole new one, which matters
// on an edge device that can lose power mid-write.
//
// The temporary file is deliberately created in the target directory rather than
// the system temp directory. /etc and /tmp are routinely separate filesystems,
// and os.Rename cannot cross one (EXDEV).
//
// The previous file is copied — not moved — to path + ".bak" beforehand, so the
// original stays readable right up to the instant it is replaced.
func Write(path string, cfg *types.Config) error {
	// A document that does not declare its schema reads back with a warning, so
	// the fields are filled in here rather than trusted to the caller. cfg itself
	// is left alone.
	doc := *cfg
	if doc.APIVersion == "" {
		doc.APIVersion = types.ConfigAPIVersion
	}
	if doc.Kind == "" {
		doc.Kind = types.ConfigKind
	}

	raw, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("serialise config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, configDirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Any failure from here on must not leave the temporary file behind.
	if err := writeAndClose(tmp, raw); err != nil {
		_ = os.Remove(tmpName) // best effort; the write has already failed
		return fmt.Errorf("write %s: %w", tmpName, err)
	}

	if err := backup(path); err != nil {
		_ = os.Remove(tmpName) // best effort; the write has already failed
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName) // best effort; the write has already failed
		return fmt.Errorf("replace %s: %w", path, err)
	}

	return nil
}

// writeAndClose writes raw to f, sets its mode and flushes it to disk. Without
// the Sync, a crash after the rename can leave a correctly named file with no
// contents: the rename may reach the disk before the data does.
func writeAndClose(f *os.File, raw []byte) (err error) {
	defer func() {
		// Close reports errors a buffered write may not have surfaced yet, so it
		// is not discarded — but it must not mask an earlier failure either.
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()

	if _, err := f.Write(raw); err != nil {
		return err
	}

	// os.CreateTemp documents mode 0600, but that is before umask, and umask
	// subtracts bits: under 0277 it yields 0400, under 0677 it yields 0000. The
	// rename below carries the temporary file's mode onto the final path, so
	// without this the config file's permissions would depend on whatever umask
	// KubeSolo inherited. See TestWriteModeIgnoresUmask.
	if err := f.Chmod(configFileMode); err != nil {
		return err
	}

	return f.Sync()
}

// backup copies the current file alongside itself. A missing file is not an
// error: the first write has nothing to preserve.
func backup(path string) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s for backup: %w", path, err)
	}

	if err := os.WriteFile(path+backupSuffix, raw, configFileMode); err != nil {
		return fmt.Errorf("write %s: %w", path+backupSuffix, err)
	}
	return nil
}
