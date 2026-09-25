// Package ui adds ANSI colors to terminal output. Colors are enabled per
// stream only when it is a terminal, NO_COLOR is unset and TERM is not "dumb";
// FORCE_COLOR turns them on regardless (e.g. for `less -R`).
package ui

import (
	"os"
	"strings"
)

// Style colors text for one output stream; the zero value prints plain text.
type Style struct{ on bool }

var (
	Out = Style{enabled(os.Stdout)}
	Err = Style{enabled(os.Stderr)}
)

func enabled(f *os.File) bool {
	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok || os.Getenv("TERM") == "dumb" {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Disable turns colors off on both streams (used by --no-color).
func Disable() { Out.on, Err.on = false, false }

func (s Style) wrap(code, text string) string {
	if !s.on || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Bold(t string) string    { return s.wrap("1", t) }
func (s Style) Dim(t string) string     { return s.wrap("2", t) }
func (s Style) Red(t string) string     { return s.wrap("31", t) }
func (s Style) Green(t string) string   { return s.wrap("32", t) }
func (s Style) Yellow(t string) string  { return s.wrap("33", t) }
func (s Style) Blue(t string) string    { return s.wrap("34", t) }
func (s Style) Magenta(t string) string { return s.wrap("35", t) }
func (s Style) Cyan(t string) string    { return s.wrap("36", t) }
func (s Style) Gray(t string) string    { return s.wrap("37", t) }

func (s Style) BoldRed(t string) string   { return s.wrap("1;31", t) }
func (s Style) BoldGreen(t string) string { return s.wrap("1;32", t) }

// Cmd highlights a command the user can copy and run.
func (s Style) Cmd(t string) string { return s.Cyan(t) }

// Label renders a field name such as "PID:" in detail views.
func (s Style) Label(t string) string { return s.Dim(t) }

// Header renders a table header row.
func (s Style) Header(t string) string { return s.wrap("1;4", strings.TrimRight(t, " ")) }
