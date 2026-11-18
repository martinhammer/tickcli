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

// symbol is a day's value on t as the today view shows it: ✓ or a count, a
// Number at its track's decimal places with its unit, a Choice's label; "·"
// for none; a value of a track of a newer type as its JSON. With --numeric,
// a Choice's place, and 1 or 0 for yes/no and every count.
func (a *App) symbol(t tickbuddy.Track, v tickbuddy.Value) string {
	switch {
	case v.IsNull():
		return "·"
	case v.Raw() != nil:
		return flat(string(v.Raw()))
	case v.IsBool() && a.opts.numeric:
		return fmt.Sprint(v.Int())
	case v.IsBool() && v.Bool():
		return "✓"
	case v.IsBool():
		return "·"
	case t.IsNumber():
		if u := flat(t.Unit); u != "" {
			return number(t, v) + " " + u
		}
		return number(t, v)
	case t.IsChoice():
		return a.choice(t, v)
	case v.Int() == 0 && !a.opts.numeric:
		return "·"
	}
	return fmt.Sprint(v.Int())
}

// plainValue is a day's value on t for plain output: yes/no or a count, a
// Number at its track's decimal places without its unit, a Choice's label,
// "" for none, a value of a track of a newer type as its JSON. With
// --numeric, a Choice's place, and 1 or 0 for yes/no.
func (a *App) plainValue(t tickbuddy.Track, v tickbuddy.Value) string {
	switch {
	case v.IsNull():
		return ""
	case v.Raw() != nil:
		return flat(string(v.Raw()))
	case v.IsBool() && a.opts.numeric:
		return fmt.Sprint(v.Int())
	case t.IsNumber():
		return number(t, v)
	case t.IsChoice():
		return a.choice(t, v)
	}
	return v.String()
}

// number is a Number's value at its track's decimal places.
func number(t tickbuddy.Track, v tickbuddy.Value) string {
	if d, ok := v.Decimal(); ok {
		return d.Format(t.DecimalPlaces)
	}
	return v.String()
}

// choice is a Choice's label, or its place with --numeric; "#5" for a
// place the track has no option for.
func (a *App) choice(t tickbuddy.Track, v tickbuddy.Value) string {
	o, ok := t.Option(v.Int())
	switch {
	case !ok:
		return fmt.Sprintf("#%d", v.Int())
	case a.opts.numeric:
		return fmt.Sprint(v.Int())
	}
	return flat(o.Label)
}

// cell is a day's value on t in week's grid: symbol without a Number's unit
// (the row's name has it) and a string without its quotes, cut to fit n
// columns, what doesn't fit ending in "…".
func (a *App) cell(t tickbuddy.Track, v tickbuddy.Value, n int) string {
	sym := a.symbol(t, v)
	var s string
	if raw := v.Raw(); len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &s) == nil {
		sym = flat(s)
	} else if t.IsNumber() && !v.IsNull() {
		sym = number(t, v)
	}
	return cut(sym, n)
}

// cut cuts s to n columns, ending in "…" when it doesn't fit.
func cut(s string, n int) string {
	if width(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// valueExtras are what JSON output adds next to a day's value on t: a
// Number track's unit (when it has one, also on a day without a value) and
// a Choice's label (with a value whose option exists).
func valueExtras(t tickbuddy.Track, v tickbuddy.Value) (unit, label *string) {
	if t.IsNumber() && t.Unit != "" {
		unit = &t.Unit
	}
	if t.IsChoice() && !v.IsNull() && v.Raw() == nil {
		if o, ok := t.Option(v.Int()); ok {
			label = &o.Label
		}
	}
	return unit, label
}

// newerLabel follows a value of a track of a type this Tickcli doesn't know
// in pretty output (newer), and is "" otherwise.
func (a *App) newerLabel(newer bool) string {
	if !newer {
		return ""
	}
	return "  " + a.paint(dim, "(Tickcli update required)")
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

// typeName is the track's type for a person: the server's name for a type
// this Tickcli doesn't know.
func typeName(t tickbuddy.Track) string {
	switch {
	case !t.Known():
		return flat(t.Type)
	case t.Type == tickbuddy.TypeYesNo:
		return "yes/no"
	}
	return t.Type
}

// settingsText is the line tracks shows under a Number or Choice track:
// "km · 1 decimal place · negative values allowed", "#1 Excited · #2 Happy ·
// ordered scale"; "" for other types.
func settingsText(t tickbuddy.Track) string {
	var parts []string
	switch {
	case t.IsNumber():
		if u := flat(t.Unit); u != "" {
			parts = append(parts, u)
		}
		switch t.DecimalPlaces {
		case 0:
			parts = append(parts, "no decimal places")
		case 1:
			parts = append(parts, "1 decimal place")
		default:
			parts = append(parts, fmt.Sprintf("%d decimal places", t.DecimalPlaces))
		}
		if t.AllowNegative {
			parts = append(parts, "negative values allowed")
		}
	case t.IsChoice():
		for _, o := range t.Options {
			parts = append(parts, fmt.Sprintf("#%d %s", o.Value, flat(o.Label)))
		}
		if t.OrderedScale {
			parts = append(parts, "ordered scale")
		}
	}
	return strings.Join(parts, " · ")
}
