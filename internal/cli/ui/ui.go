// Package ui provides lightweight terminal output primitives for kubesoloctl.
// It respects NO_COLOR and TERM=dumb and degrades to plain ASCII in non-TTY
// environments (CI pipelines, serial consoles, piped output).
package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// ANSI escape sequences — only emitted when colour is enabled.
const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiCyan   = "\033[36m"
	ansiGray   = "\033[90m"
)

// Printer writes structured, human-readable output to stderr. In a
// colour-capable TTY it uses ANSI sequences and Unicode symbols; in non-TTY
// or NO_COLOR environments it falls back to plain ASCII. All symbols and the
// message column are aligned at a consistent indent so that zerolog detail
// lines (indented 5 spaces via the custom ConsoleWriter in configureLogging)
// nest cleanly under each Printer step.
type Printer struct {
	w     io.Writer
	color bool
}

// New returns a Printer targeting os.Stderr with auto-detected colour support.
func New() *Printer {
	return &Printer{w: os.Stderr, color: ColorEnabled()}
}

// ColorEnabled reports whether ANSI colour output is appropriate for the
// current stderr. Exported so configureLogging can share the same detection.
func ColorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// ErrAlreadyReported is returned by Fail so callers can propagate a non-nil
// error to cobra (causing exit 1) without cobra reprinting the message.
// main.go checks err.Error() == "" to distinguish it from real errors.
var ErrAlreadyReported = fmt.Errorf("")

// fprintf writes to the printer's writer, ignoring write errors — the target is
// a terminal or buffer where writes do not meaningfully fail.
func (p *Printer) fprintf(format string, a ...any) {
	_, _ = fmt.Fprintf(p.w, format, a...)
}

// Header prints a command banner at the start of a kubesoloctl command.
func (p *Printer) Header(cmd string) {
	title := "kubesoloctl  " + cmd
	bar := strings.Repeat("─", len(title)+4)
	if p.color {
		p.fprintf("\n  %s%s%s\n  %s%s%s\n",
			ansiBold, title, ansiReset,
			ansiGray, bar, ansiReset)
	} else {
		p.fprintf("\n  %s\n  %s\n", title, bar)
	}
}

// Step announces a stage that is about to run. Call it before a potentially
// slow operation so the user has immediate visual feedback.
func (p *Printer) Step(msg string) {
	if p.color {
		p.fprintf("\n  %s▸%s  %s\n", ansiCyan, ansiReset, msg)
	} else {
		p.fprintf("\n  > %s\n", msg)
	}
}

// OK marks a completed stage. detail is optional trailing context (path,
// version, …); pass "" to omit. Text is aligned so the message starts at
// the same column as zerolog detail lines.
func (p *Printer) OK(msg, detail string) {
	if p.color {
		if detail != "" {
			p.fprintf("  %s✓%s  %-40s%s%s%s\n",
				ansiGreen, ansiReset, msg, ansiGray, detail, ansiReset)
		} else {
			p.fprintf("  %s✓%s  %s\n", ansiGreen, ansiReset, msg)
		}
	} else {
		if detail != "" {
			p.fprintf("  [ok] %-40s%s\n", msg, detail)
		} else {
			p.fprintf("  [ok] %s\n", msg)
		}
	}
}

// Fail prints a failure indicator for msg, reports the error, and returns
// ErrAlreadyReported. Use in return statements: return p.Fail("stage", err)
func (p *Printer) Fail(msg string, err error) error {
	if p.color {
		p.fprintf("  %s✗%s  %s: %v\n", ansiRed, ansiReset, msg, err)
	} else {
		p.fprintf("  [fail] %s: %v\n", msg, err)
	}
	return ErrAlreadyReported
}

// Warn prints a non-fatal warning.
func (p *Printer) Warn(msg string) {
	if p.color {
		p.fprintf("  %s⚠%s   %s\n", ansiYellow, ansiReset, msg)
	} else {
		p.fprintf("  [warn] %s\n", msg)
	}
}

// Info prints a plain indented line — use for next-steps, tips, or shell
// snippets shown at the end of a command.
func (p *Printer) Info(msg string) {
	p.fprintf("     %s\n", msg)
}

// Done prints the final success message for a command, bold green in colour mode.
func (p *Printer) Done(msg string) {
	if p.color {
		p.fprintf("\n  %s%s%s\n\n", ansiBold+ansiGreen, msg, ansiReset)
	} else {
		p.fprintf("\n  %s\n\n", msg)
	}
}

// Section prints a labelled group header — use for "Next steps" blocks.
func (p *Printer) Section(title string) {
	if p.color {
		p.fprintf("\n  %s%s%s\n", ansiGray, title, ansiReset)
	} else {
		p.fprintf("\n  %s\n", title)
	}
}

// Hint prints a labelled tip block with indented shell-snippet lines.
func (p *Printer) Hint(label string, lines ...string) {
	if p.color {
		p.fprintf("\n  %s%s%s\n", ansiGray, label, ansiReset)
	} else {
		p.fprintf("\n  %s\n", label)
	}
	for _, l := range lines {
		p.fprintf("     %s\n", l)
	}
}

// Cmd prints a primary shell command at a shallow indent, highlighted in colour
// mode. Use for the first actionable commands to run after a successful install.
func (p *Printer) Cmd(msg string) {
	if p.color {
		p.fprintf("  %s%s%s\n", ansiCyan, msg, ansiReset)
	} else {
		p.fprintf("  %s\n", msg)
	}
}

// Label prints a right-padded label and value pair on a single line, with the
// label dimmed in colour mode. Use for compact command-reference tables such as
// "Manage  systemctl status kubesolo".
func (p *Printer) Label(label, value string) {
	if p.color {
		p.fprintf("  %s%-8s%s %s\n", ansiGray, label, ansiReset, value)
	} else {
		p.fprintf("  %-8s %s\n", label, value)
	}
}

// Blank emits a bare newline — use to separate visual groups in footers.
func (p *Printer) Blank() {
	_, _ = fmt.Fprintln(p.w)
}
