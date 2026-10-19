// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Date is a calendar date with no time or zone. The zero Date is "no date".
// It formats, and is sent to the server, as YYYY-MM-DD.
type Date struct {
	y int
	m time.Month
	d int
}

// NewDate returns the date, or ErrBadDate when it doesn't exist (2026-02-30).
func NewDate(year int, month time.Month, day int) (Date, error) {
	t := time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
	if t.Year() != year || t.Month() != month || t.Day() != day {
		return Date{}, fmt.Errorf("%w: %04d-%02d-%02d doesn't exist", ErrBadDate, year, int(month), day)
	}
	return Date{year, month, day}, nil
}

// DateOf returns the calendar date of t in t's own location.
func DateOf(t time.Time) Date {
	return Date{t.Year(), t.Month(), t.Day()}
}

// Today is today's date by the machine's clock and local time zone, as the
// web and phone clients count it.
func Today() Date { return DateOf(time.Now()) }

// IsZero reports whether d is the zero Date.
func (d Date) IsZero() bool { return d == Date{} }

// Year, Month and Day return the parts of the date.
func (d Date) Year() int             { return d.y }
func (d Date) Month() time.Month     { return d.m }
func (d Date) Day() int              { return d.d }
func (d Date) Weekday() time.Weekday { return d.noon().Weekday() }

// Time returns midnight of the date in loc.
func (d Date) Time(loc *time.Location) time.Time {
	return time.Date(d.y, d.m, d.d, 0, 0, 0, 0, loc)
}

// noon in UTC keeps day arithmetic clear of DST changes.
func (d Date) noon() time.Time { return time.Date(d.y, d.m, d.d, 12, 0, 0, 0, time.UTC) }

// AddDays returns the date n days later (earlier when n is negative).
func (d Date) AddDays(n int) Date { return DateOf(d.noon().AddDate(0, 0, n)) }

// Compare returns -1, 0 or +1 as d is before, equal to or after o.
func (d Date) Compare(o Date) int { return d.noon().Compare(o.noon()) }

// Before reports whether d is before o.
func (d Date) Before(o Date) bool { return d.Compare(o) < 0 }

// String formats the date as YYYY-MM-DD, or "" for the zero Date.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.y, int(d.m), d.d)
}

// MarshalJSON writes "YYYY-MM-DD", or null for the zero Date.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON reads "YYYY-MM-DD" or null.
func (d *Date) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*d = Date{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := parseISODate(s)
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// ParseDate reads a date as a person or script writes it, relative to today:
//
//   - "today" and "yesterday"
//   - "-N", N days ago ("-0" is today)
//   - a real date as YYYY-MM-DD
//
// Anything else is ErrBadDate.
func ParseDate(s string, today Date) (Date, error) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "today":
		return today, nil
	case "yesterday":
		return today.AddDays(-1), nil
	}
	if rest, ok := strings.CutPrefix(s, "-"); ok && rest != "" && allDigits(rest) {
		n, err := strconv.Atoi(rest)
		if err != nil || n > 100000 {
			return Date{}, fmt.Errorf("%w: %q is too far back", ErrBadDate, s)
		}
		return today.AddDays(-n), nil
	}
	return parseISODate(s)
}

func parseISODate(s string) (Date, error) {
	bad := fmt.Errorf("%w: %q (use YYYY-MM-DD, today, yesterday or -N for N days ago)", ErrBadDate, s)
	if len(s) != 10 || s[4] != '-' || s[7] != '-' || !allDigits(s[:4]) || !allDigits(s[5:7]) || !allDigits(s[8:]) {
		return Date{}, bad
	}
	y, _ := strconv.Atoi(s[:4])
	m, _ := strconv.Atoi(s[5:7])
	d, _ := strconv.Atoi(s[8:])
	v, err := NewDate(y, time.Month(m), d)
	if err != nil {
		return Date{}, bad
	}
	return v, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
