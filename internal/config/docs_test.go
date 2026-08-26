package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func readDoc(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "configuration", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestEverySettingIsDocumented walks the registry rather than the documentation,
// so a setting added without a line in the reference fails here instead of
// quietly shipping undocumented.
//
// It matches the settings table's own row shape rather than the path anywhere in
// the file. Searching the whole document would be satisfied by the migration
// table further down, and deleting a row from the reference would go unnoticed.
func TestEverySettingIsDocumented(t *testing.T) {
	doc := readDoc(t, "config-file.md")

	for _, d := range Describe() {
		row := "| `" + d.Path + "` | `" + d.Type + "` |"
		if !strings.Contains(doc, row) {
			t.Errorf("config-file.md has no settings-table row for %s:\n  want a line starting: %s", d.Path, row)
		}
	}
}

// TestEveryFlagIsInTheMigrationTable keeps the flag-to-setting mapping complete.
// It is the table an operator reads while converting an existing install, so a
// gap in it is a gap in the migration.
func TestEveryFlagIsInTheMigrationTable(t *testing.T) {
	doc := readDoc(t, "config-file.md")

	for _, f := range Registry() {
		if f.Flag == "" {
			continue
		}
		row := "| `--" + f.Flag + "` | `" + f.Envar + "` | `" + f.ConfigPath + "` |"
		if !strings.Contains(doc, row) {
			t.Errorf("config-file.md is missing the migration row for --%s:\n  want: %s", f.Flag, row)
		}
	}
}

// TestDocumentedDefaultsMatchTheCode catches the reference drifting from the
// values it claims to describe — the most likely way this documentation goes
// stale, and the least likely to be noticed.
func TestDocumentedDefaultsMatchTheCode(t *testing.T) {
	doc := readDoc(t, "config-file.md")

	for _, d := range Describe() {
		if d.Default == nil {
			continue // shown as an em dash; nothing to compare
		}

		var want string
		switch v := d.Default.(type) {
		case string:
			if v == "" {
				continue // rendered as "" and not worth matching on
			}
			want = v
		case bool:
			if !v {
				continue // false is ubiquitous in the document
			}
			want = "true"
		case int:
			if v == 0 {
				continue
			}
			want = strconv.Itoa(v)
		default:
			continue
		}

		row := "| `" + d.Path + "` | `" + d.Type + "` | `" + want + "` |"
		if !strings.Contains(doc, row) {
			t.Errorf("config-file.md records the wrong default for %s:\n  want: %s", d.Path, row)
		}
	}
}

// TestSecretsAreFlaggedInTheDocs — an operator has to know which values are
// credentials, because that governs how the file may be handled.
func TestSecretsAreFlaggedInTheDocs(t *testing.T) {
	doc := readDoc(t, "config-file.md")

	for _, f := range Registry() {
		if !f.Secret {
			continue
		}
		if !strings.Contains(doc, "`"+f.ConfigPath+"`") || !strings.Contains(doc, "*(secret)*") {
			t.Errorf("config-file.md does not mark %s as a secret", f.ConfigPath)
		}
	}
}

// TestAPIDocumentsEveryRoute pins the API reference against the routes actually
// served, by name.
func TestAPIDocumentsEveryRoute(t *testing.T) {
	doc := readDoc(t, "config-api.md")

	for _, route := range []string{
		"`GET` | `/api/v1/config`",
		"`GET` | `/api/v1/config/schema`",
		"`PUT` | `/api/v1/config`",
		"`PATCH` | `/api/v1/config`",
		"`POST` | `/api/v1/config:validate`",
		"`DELETE` | `/api/v1/config`",
		"`GET` | `/healthz`",
	} {
		if !strings.Contains(doc, route) {
			t.Errorf("config-api.md does not document %s", route)
		}
	}
}
