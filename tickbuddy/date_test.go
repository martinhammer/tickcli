// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"errors"
	"testing"
	"time"
)

func TestParseDate(t *testing.T) {
	today := Date{2026, time.March, 1}
	good := map[string]string{
		"today":        "2026-03-01",
		"Yesterday":    "2026-02-28",
		"-1":           "2026-02-28",
		"-0":           "2026-03-01",
		"-365":         "2025-03-01",
		"2026-02-28":   "2026-02-28",
		"2024-02-29":   "2024-02-29",
		" 2026-10-18 ": "2026-10-18",
	}
	for in, want := range good {
		d, err := ParseDate(in, today)
		if err != nil || d.String() != want {
			t.Errorf("ParseDate(%q) = %v, %v; want %s", in, d, err, want)
		}
	}
	for _, in := range []string{"2026-02-30", "2025-02-29", "+1", "-", "-x", "tomorrow", "", "18.10.2026", "2026-1-18", "-999999999999"} {
		if _, err := ParseDate(in, today); !errors.Is(err, ErrBadDate) {
			t.Errorf("ParseDate(%q): err = %v, want ErrBadDate", in, err)
		}
	}
}

func TestDateArithmetic(t *testing.T) {
	d := Date{2026, time.March, 29} // DST change in Europe
	if got := d.AddDays(1).String(); got != "2026-03-30" {
		t.Errorf("AddDays = %s", got)
	}
	if got := d.AddDays(-29).String(); got != "2026-02-28" {
		t.Errorf("AddDays = %s", got)
	}
	if d.Weekday() != time.Sunday {
		t.Errorf("weekday %v", d.Weekday())
	}
	if !d.Before(d.AddDays(1)) || d.Compare(d) != 0 {
		t.Error("Compare")
	}
}
