// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"errors"
	"testing"
)

func TestResolve(t *testing.T) {
	archived := Date{2026, 10, 1}
	tracks := []Track{
		{ID: 1, Name: "Exercise", Type: TypeYesNo},
		{ID: 2, Name: "Cups of coffee", Type: TypeCounter},
		{ID: 3, Name: "Nightly backup", Type: TypeYesNo, ArchivedAt: &archived},
		{ID: 4, Name: "Diary", Type: TypeYesNo, Private: true},
		{ID: 5, Name: "Cups of tea", Type: TypeCounter},
		{ID: 6, Name: "Twin", Type: TypeYesNo},
		{ID: 7, Name: "twin ", Type: TypeYesNo},
	}
	ok := []struct {
		q           string
		interactive bool
		id          int
	}{
		{"#2", false, 2},
		{"exercise", false, 1},
		{"  EXERCISE ", false, 1},
		{"nightly backup", false, 3}, // archived still matches
		{"diary", false, 4},          // private too
		{"exer", true, 1},
		{"cups of c", true, 2},
	}
	for _, tc := range ok {
		got, err := Resolve(tracks, tc.q, tc.interactive)
		if err != nil || got.ID != tc.id {
			t.Errorf("Resolve(%q, %v) = %d, %v; want %d", tc.q, tc.interactive, got.ID, err, tc.id)
		}
	}

	fail := []struct {
		q           string
		interactive bool
		want        error
		candidates  int
	}{
		{"exer", false, ErrTrackNotFound, 1}, // no prefixes for scripts
		{"cups", true, ErrAmbiguous, 2},
		{"twin", false, ErrAmbiguous, 2},
		{"#99", false, ErrTrackNotFound, 0},
		{"walk", true, ErrTrackNotFound, 0},
		{"coffee", false, ErrTrackNotFound, 1},
	}
	for _, tc := range fail {
		_, err := Resolve(tracks, tc.q, tc.interactive)
		var me *MatchError
		if !errors.Is(err, tc.want) || !errors.As(err, &me) || len(me.Candidates) != tc.candidates {
			t.Errorf("Resolve(%q, %v) = %v; want %v with %d candidates", tc.q, tc.interactive, err, tc.want, tc.candidates)
		}
	}
	_, err := Resolve(tracks, "cups", true)
	if want := `"cups" matches several tracks: Cups of coffee (#2), Cups of tea (#5)`; err.Error() != want {
		t.Errorf("message %q", err)
	}
}
