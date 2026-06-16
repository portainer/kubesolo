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

// Header prints a command banner at the start of a kubesoloctl command.
func (p *Printer) Header(cmd string) {
	title := "kubesoloctl  " + cmd
	bar := strings.Repeat("─", len(title)+4)
	if p.color {
		fmt.Fprintf(p.w, "\n  %s%s%s\n  %s%s%s\n",
			ansiBold, title, ansiReset,
			ansiGray, bar, ansiReset)
	} else {
		fmt.Fprintf(p.w, "\n  %s\n  %s\n", title, bar)
	}
}

// Step announces a stage that is about to run. Call it before a potentially
// slow operation so the user has immediate visual feedback.
func (p *Printer) Step(msg string) {
	if p.color {
		fmt.Fprintf(p.w, "\n  %s▸%s  %s\n", ansiCyan, ansiReset, msg)
	} else {
		fmt.Fprintf(p.w, "\n  > %s\n", msg)
	}
}

// OK marks a completed stage. detail is optional trailing context (path,
// version, …); pass "" to omit. Text is aligned so the message starts at
// the same column as zerolog detail lines.
func (p *Printer) OK(msg, detail string) {
	if p.color {
		if detail != "" {
			fmt.Fprintf(p.w, "  %s✓%s  %-40s%s%s%s\n",
				ansiGreen, ansiReset, msg, ansiGray, detail, ansiReset)
		} else {
			fmt.Fprintf(p.w, "  %s✓%s  %s\n", ansiGreen, ansiReset, msg)
		}
	} else {
		if detail != "" {
			fmt.Fprintf(p.w, "  [ok] %-40s%s\n", msg, detail)
		} else {
			fmt.Fprintf(p.w, "  [ok] %s\n", msg)
		}
	}
}

// Fail prints a failure indicator for msg, reports the error, and returns
// ErrAlreadyReported. Use in return statements: return p.Fail("stage", err)
func (p *Printer) Fail(msg string, err error) error {
	if p.color {
		fmt.Fprintf(p.w, "  %s✗%s  %s: %v\n", ansiRed, ansiReset, msg, err)
	} else {
		fmt.Fprintf(p.w, "  [fail] %s: %v\n", msg, err)
	}
	return ErrAlreadyReported
}

// Warn prints a non-fatal warning.
func (p *Printer) Warn(msg string) {
	if p.color {
		fmt.Fprintf(p.w, "  %s⚠%s   %s\n", ansiYellow, ansiReset, msg)
	} else {
		fmt.Fprintf(p.w, "  [warn] %s\n", msg)
	}
}

// Info prints a plain indented line — use for next-steps, tips, or shell
// snippets shown at the end of a command.
func (p *Printer) Info(msg string) {
	fmt.Fprintf(p.w, "     %s\n", msg)
}

// Done prints the final success message for a command, bold green in colour mode.
func (p *Printer) Done(msg string) {
	if p.color {
		fmt.Fprintf(p.w, "\n  %s%s%s\n\n", ansiBold+ansiGreen, msg, ansiReset)
	} else {
		fmt.Fprintf(p.w, "\n  %s\n\n", msg)
	}
}

// Section prints a labelled group header — use for "Next steps" blocks.
func (p *Printer) Section(title string) {
	if p.color {
		fmt.Fprintf(p.w, "\n  %s%s%s\n", ansiGray, title, ansiReset)
	} else {
		fmt.Fprintf(p.w, "\n  %s\n", title)
	}
}

// Hint prints a labelled tip block with indented shell-snippet lines.
func (p *Printer) Hint(label string, lines ...string) {
	if p.color {
		fmt.Fprintf(p.w, "\n  %s%s%s\n", ansiGray, label, ansiReset)
	} else {
		fmt.Fprintf(p.w, "\n  %s\n", label)
	}
	for _, l := range lines {
		fmt.Fprintf(p.w, "     %s\n", l)
	}
}
