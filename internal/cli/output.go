// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/martinhammer/tickcli/tickbuddy"
)

// mode is how output is written.
type mode int

const (
	pretty   mode = iota // a terminal
	plain                // piped: tab-separated lines
	jsonMode             // --json
)

func (a *App) mode() mode {
	switch {
	case a.opts.json:
		return jsonMode
	case a.StdoutTTY:
		return pretty
	}
	return plain
}

// colour reports whether pretty output may use ANSI colours.
func (a *App) colour() bool {
	return a.mode() == pretty && a.Getenv("NO_COLOR") == "" && a.Getenv("TERM") != "dumb"
}

const (
	green = "32"
	dim   = "2"
	bold  = "1"
	red   = "31"
)

func (a *App) paint(code, s string) string {
	if !a.colour() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func (a *App) printf(format string, args ...any) {
	fmt.Fprintf(a.Stdout, format, args...)
}

func (a *App) writeJSON(v any) error { return newJSONEncoder(a.Stdout).Encode(v) }

func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc
}

// clean drops what could steer a terminal from a string the server sent:
// C0 controls except tab and line break, DEL, C1 controls, and invalid
// UTF-8 or lone surrogates (which JSON decoding turns into U+FFFD).
func clean(s string) string {
	ok := true
	for _, r := range s {
		if dropRune(r) {
			ok = false
			break
		}
	}
	if ok && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if !dropRune(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func dropRune(r rune) bool {
	switch {
	case r == '\t' || r == '\n':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == utf8.RuneError:
		return true
	}
	return false
}

// flat is clean for one field of a tab-separated line: tabs and line breaks
// become spaces.
func flat(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(clean(s))
}

// noteLines is a note's lines, cleaned for a terminal.
func noteLines(s string) []string {
	return strings.Split(strings.TrimRight(clean(s), "\n"), "\n")
}

// The day as pretty output shows it. Go has no locale data, so names are
// English.
func longDay(d tickbuddy.Date) string  { return d.Time(time.Local).Format("Mon 2 Jan 2006") }
func shortDay(d tickbuddy.Date) string { return d.Time(time.Local).Format("Mon 2 Jan") }
func dayMonth(d tickbuddy.Date) string { return d.Time(time.Local).Format("2 Jan") }
func dayMonthYear(d tickbuddy.Date) string {
	return d.Time(time.Local).Format("2 Jan 2006")
}

// pad pads s with spaces to width runes.
func pad(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func width(s string) int { return utf8.RuneCountInString(s) }

// symbol is a day's value as the today view shows it.
func symbol(v tickbuddy.Value) string {
	if v.IsBool() {
		if v.Bool() {
			return "✓"
		}
		return "·"
	}
	if v.Int() == 0 {
		return "·"
	}
	return fmt.Sprint(v.Int())
}

func (a *App) paintSymbol(sym string) string {
	switch sym {
	case "✓":
		return a.paint(green, sym)
	case "·":
		return a.paint(dim, sym)
	}
	return sym
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// runText is the current run, worded as the dashboard widget words a streak
// that ends yesterday.
func runText(st *tickbuddy.Stats) string {
	if st == nil || st.Current == nil {
		return "no ticks yet"
	}
	c := st.Current
	if c.IsStreak {
		if st.UpToYesterday {
			return fmt.Sprintf("%d-day streak, up to yesterday", c.Length)
		}
		return fmt.Sprintf("%d-day streak", c.Length)
	}
	return fmt.Sprintf("%d-day break", c.Length)
}

// runPlain is the current run for plain output: "streak 3", "break 1" or
// "none".
func runPlain(st *tickbuddy.Stats) string {
	if st == nil || st.Current == nil {
		return "none"
	}
	if st.Current.IsStreak {
		return fmt.Sprintf("streak %d", st.Current.Length)
	}
	return fmt.Sprintf("break %d", st.Current.Length)
}

func typeName(t tickbuddy.Track) string {
	if t.IsCounter() {
		return "counter"
	}
	return "yes/no"
}
