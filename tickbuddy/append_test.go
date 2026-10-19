// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

const (
	setPath       = "POST /ocs/v2.php/apps/tickbuddy/api/ticks/set"
	incrementPath = "POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment"
	appendPath    = "POST /ocs/v2.php/apps/tickbuddy/api/notes/append"
)

// lastBody decodes the body of the last request to a path ending in suffix.
func lastBody(t *testing.T, s *tickbuddytest.Server, suffix string) map[string]any {
	t.Helper()
	bodies := s.LoggedBodies(suffix)
	if len(bodies) == 0 {
		t.Fatalf("no request to %s", suffix)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(bodies[len(bodies)-1]), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNoteEditBody(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	cof := s.AddTrack("Coffee", "counter")

	cases := []struct {
		name string
		note *NoteEdit
		want map[string]any // the note fields sent, nil for absent
	}{
		{"nil", nil, map[string]any{"note": nil, "appendNote": nil, "allowDuplicateNote": nil}},
		{"replace", ReplaceNote("x"), map[string]any{"note": "x", "appendNote": nil, "allowDuplicateNote": nil}},
		{"append", AppendNote("y"), map[string]any{"note": nil, "appendNote": "y", "allowDuplicateNote": nil}},
		{"append duplicate", &NoteEdit{Text: "y", Append: true, AllowDuplicate: true}, map[string]any{"note": nil, "appendNote": "y", "allowDuplicateNote": true}},
	}
	for _, tc := range cases {
		if _, err := c.SetCount(ctx, cof, day, 1, tc.note); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := lastBody(t, s, "/ticks/set")
		for k, want := range tc.want {
			if v, ok := got[k]; want == nil && ok || want != nil && v != want {
				t.Errorf("%s: %s = %v, want %v", tc.name, k, v, want)
			}
		}
	}

	if _, err := c.AppendNote(ctx, cof, day, "z", true); err != nil {
		t.Fatal(err)
	}
	if got := lastBody(t, s, "/notes/append"); got["text"] != "z" || got["allowDuplicate"] != true || got["trackId"] != float64(cof) || got["date"] != day.String() {
		t.Errorf("append body = %v", got)
	}
	if _, err := c.AppendNote(ctx, cof, day, "z", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := lastBody(t, s, "/notes/append")["allowDuplicate"]; ok {
		t.Error("allowDuplicate sent without being set")
	}
}

func TestAppendNote(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")

	for _, text := range []string{"a", "a", " b\r\n"} {
		if _, err := c.AppendNote(ctx, ex, day, text, false); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.AppendNote(ctx, ex, day, "a", false)
	if err != nil || got != "a\nb" {
		t.Fatalf("AppendNote = %q, %v", got, err)
	}
	if got, _ := c.AppendNote(ctx, ex, day, "a", true); got != "a\nb\na" {
		t.Errorf("allowDuplicate = %q", got)
	}

	r, err := c.SetYesNo(ctx, ex, day, true, AppendNote("c"))
	if err != nil || r.Note == nil || *r.Note != "a\nb\na\nc" || r.Archived || !r.Value.Bool() {
		t.Fatalf("SetYesNo append = %+v %v", r, err)
	}
	r, err = c.SetYesNo(ctx, ex, day, true, AppendNote("c"))
	if err != nil || *r.Note != "a\nb\na\nc" || r.Archived {
		t.Fatalf("SetYesNo append again = %+v %v", r, err)
	}

	s.Update(ex, func(t *tickbuddytest.Track) { t.ArchivedAt = "2026-10-01" })
	if got, err := c.AppendNote(ctx, ex, day, "d", false); err != nil || NoteContains(got, "d") {
		t.Errorf("archived AppendNote = %q, %v", got, err)
	}
	cof := s.AddTrack("Coffee", "counter")
	s.Update(cof, func(t *tickbuddytest.Track) { t.ArchivedAt = "2026-10-01" })
	if r, err := c.Add(ctx, cof, day, 1, AppendNote("d")); err != nil || !r.Archived {
		t.Errorf("archived Add with append = %+v %v", r, err)
	}
}

func TestNoteContains(t *testing.T) {
	for _, tc := range []struct {
		note, text string
		want       bool
	}{
		{"a\nb\nc", "b", true},
		{"a\nb\nc", "b\nc", true},
		{"a\nb\nc", " a\r\nb ", true},
		{"a\nb\nc", "a\nc", false},
		{"ab", "a", false},
		{"", "a", false},
		{"", " ", true},
	} {
		if got := NoteContains(tc.note, tc.text); got != tc.want {
			t.Errorf("NoteContains(%q, %q) = %v", tc.note, tc.text, got)
		}
	}
}

func TestAppendMissingFlag(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Coffee", "counter")
	s.Features[FeatureNotesAppend] = false
	before := len(s.Logged())

	for name, f := range map[string]func() error{
		"AppendNote": func() error { _, err := c.AppendNote(ctx, ex, day, "x", false); return err },
		"SetYesNo":   func() error { _, err := c.SetYesNo(ctx, ex, day, true, AppendNote("x")); return err },
		"SetCount":   func() error { _, err := c.SetCount(ctx, cof, day, 1, AppendNote("x")); return err },
		"Add":        func() error { _, err := c.Add(ctx, cof, day, 1, AppendNote("x")); return err },
	} {
		var fe *FeatureError
		if err := f(); !errors.As(err, &fe) || fe.Feature != FeatureNotesAppend {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, r := range s.Logged()[before:] {
		if !strings.Contains(r, "capabilities") {
			t.Errorf("sent %s despite the missing flag", r)
		}
	}
	// replacing still works
	if _, err := c.SetYesNo(ctx, ex, day, true, ReplaceNote("x")); err != nil {
		t.Errorf("replace without notesAppend: %v", err)
	}
}

func TestAppendRetries(t *testing.T) {
	s, c, waits := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Coffee", "counter")
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}

	// AppendNote: a 409 and an outage are retried
	s.ConflictNext = 1
	if got, err := c.AppendNote(ctx, ex, day, "a", false); err != nil || got != "a" {
		t.Fatalf("after a 409: %q %v", got, err)
	}
	if n := s.Count(appendPath); n != 2 || len(*waits) != 1 || (*waits)[0] != time.Second {
		t.Errorf("sent %d times, waits %v", n, *waits)
	}
	s.ApplyThenFail = 1
	if got, err := c.AppendNote(ctx, ex, day, "b", false); err != nil || got != "a\nb" {
		t.Fatalf("after a lost answer: %q %v", got, err)
	}

	// SetCount with an append is retried; the line lands once
	s.ApplyThenFail = 1
	if r, err := c.SetCount(ctx, cof, day, 2, AppendNote("x")); err != nil || *r.Note != "x" {
		t.Fatalf("SetCount after a lost answer: %+v %v", r, err)
	}
	if n := s.Count(setPath); n != 2 {
		t.Errorf("set sent %d times", n)
	}

	// with AllowDuplicate: never retried
	dup := &NoteEdit{Text: "x", Append: true, AllowDuplicate: true}
	s.ApplyThenFail = 1
	if _, err := c.SetCount(ctx, cof, day, 2, dup); !errors.Is(err, ErrUnreachable) {
		t.Errorf("SetCount duplicate: %v", err)
	}
	if n := s.Count(setPath); n != 3 {
		t.Errorf("set sent %d times", n)
	}
	before := s.Count(appendPath)
	s.ConflictNext = 1
	if _, err := c.AppendNote(ctx, cof, day, "x", true); err == nil {
		t.Error("AppendNote duplicate after a 409 succeeded")
	}
	if n := s.Count(appendPath) - before; n != 1 {
		t.Errorf("duplicate append sent %d times", n)
	}

	// Add with an append: never retried
	s.ConflictNext = 0
	s.ApplyThenFail = 1
	before = s.Count(incrementPath)
	if _, err := c.Add(ctx, cof, day, 1, AppendNote("y")); !errors.Is(err, ErrUnreachable) {
		t.Errorf("add: %v", err)
	}
	if n := s.Count(incrementPath) - before; n != 1 {
		t.Errorf("add sent %d times", n)
	}
}

func TestTickConflict(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Coffee", "counter")
	d := day.String()

	// SetCount: the 409 comes after the count was written
	s.ConflictNext = 1
	r, err := c.SetCount(ctx, cof, day, 4, AppendNote("a"))
	if err != nil || r.Value.Int() != 4 || r.Note == nil || *r.Note != "a" || r.Archived {
		t.Fatalf("SetCount = %+v %v", r, err)
	}
	if s.Count(setPath) != 1 || s.Count(appendPath) != 1 {
		t.Errorf("requests: %v", s.Logged())
	}

	// Add: not repeated, so it counts once; the count is read back
	s.ConflictNext = 1
	r, err = c.Add(ctx, cof, day, 2, AppendNote("b"))
	if err != nil || r.Value.Int() != 6 || *r.Note != "a\nb" || r.Archived {
		t.Fatalf("Add = %+v %v", r, err)
	}
	if s.Count(incrementPath) != 1 || s.Count(appendPath) != 2 || s.Tick(cof, d) != 6 {
		t.Errorf("increment %d, append %d, count %d", s.Count(incrementPath), s.Count(appendPath), s.Tick(cof, d))
	}

	// SetYesNo, and the append after it retried through another 409
	s.ConflictNext = 2
	r, err = c.SetYesNo(ctx, ex, day, true, AppendNote("c"))
	if err != nil || !r.Value.Bool() || *r.Note != "c" {
		t.Fatalf("SetYesNo = %+v %v", r, err)
	}
	if s.Count(setPath) != 2 || s.Count(appendPath) != 4 {
		t.Errorf("set %d, append %d", s.Count(setPath), s.Count(appendPath))
	}

	// other errors pass through
	if _, err := c.SetCount(ctx, 99, day, 1, AppendNote("x")); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("404: %v", err)
	}
}

func TestAppendFallback(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	s.Features[FeatureBooleanSet] = false
	s.SetNote(ex, day.String(), "a")

	// toggles, carrying the append
	r, err := c.SetYesNo(ctx, ex, day, true, AppendNote("b"))
	if err != nil || !r.Value.Bool() || *r.Note != "a\nb" {
		t.Fatalf("toggle = %+v %v", r, err)
	}
	if got := lastBody(t, s, "/ticks/toggle"); got["appendNote"] != "b" {
		t.Errorf("toggle body = %v", got)
	}
	// already ticked: appends on its own
	r, err = c.SetYesNo(ctx, ex, day, true, AppendNote("c"))
	if err != nil || *r.Note != "a\nb\nc" || s.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/toggle") != 1 || s.Count(appendPath) != 1 {
		t.Fatalf("already ticked = %+v %v, %v", r, err, s.Logged())
	}
}
