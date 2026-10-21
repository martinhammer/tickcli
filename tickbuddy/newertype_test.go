// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestValueRaw: anything but a boolean or a whole number is kept as sent.
func TestValueRaw(t *testing.T) {
	for _, in := range []string{`7.5`, `"red"`, `{"a": [1, 2]}`, `null`, `7.0`, `1e3`} {
		var v Value
		if err := json.Unmarshal([]byte(in), &v); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		var want bytes.Buffer
		_ = json.Compact(&want, []byte(in))
		if string(v.Raw()) != want.String() || v.String() != want.String() || v.IsBool() || v.Int() != 0 {
			t.Errorf("%s: Raw %s, String %q", in, v.Raw(), v)
		}
		if b, _ := json.Marshal(v); string(b) != want.String() {
			t.Errorf("%s marshals to %s", in, b)
		}
	}
	for _, in := range []string{`true`, `false`, `7`, `-2`} {
		var v Value
		if err := json.Unmarshal([]byte(in), &v); err != nil || v.Raw() != nil {
			t.Errorf("%s: Raw %s, %v", in, v.Raw(), err)
		}
	}
}

func TestValueFor(t *testing.T) {
	yesNo, counter, future := Track{Type: TypeYesNo}, Track{Type: TypeCounter}, Track{Type: "future"}
	if !yesNo.KnownType() || !counter.KnownType() || future.KnownType() {
		t.Error("KnownType")
	}
	for _, tc := range []struct {
		t    Track
		v    Value
		want string
	}{
		{yesNo, IntValue(1), "true"}, {counter, IntValue(3), "3"},
		{future, IntValue(7), "7"}, {future, BoolValue(true), "true"},
		{future, rawValue([]byte(`"red"`)), `"red"`},
	} {
		got := ValueFor(tc.t, tc.v)
		if b, _ := json.Marshal(got); string(b) != tc.want {
			t.Errorf("ValueFor(%s, %v) = %s", tc.t.Type, tc.v, b)
		}
		if !tc.t.KnownType() && (got.Raw() == nil || got.String() != tc.want) {
			t.Errorf("ValueFor(%s, %v): Raw %s, String %q", tc.t.Type, tc.v, got.Raw(), got)
		}
	}
	for tr, want := range map[Track]string{yesNo: "false", counter: "0", future: "null"} {
		if b, _ := json.Marshal(EmptyValue(tr)); string(b) != want {
			t.Errorf("EmptyValue(%s) = %s", tr.Type, b)
		}
	}
}

// TestNewerType: a track of a type this package doesn't know comes through
// Tracks, Ticks and Notes, which ask for every type; its values don't stop
// the others' from decoding.
func TestNewerType(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", TypeYesNo)
	mood := s.AddTrack("Mood", "future")
	s.SetTick(ex, "2026-10-17", 1)
	s.SetValue(mood, "2026-10-16", 7)
	s.SetValue(mood, "2026-10-17", 7.5)
	s.SetValue(mood, "2026-10-18", "red")
	s.SetNote(mood, "2026-10-18", "grumpy")

	tracks, err := c.Tracks(ctx, false)
	if err != nil || len(tracks) != 2 || tracks[1].Type != "future" || tracks[1].KnownType() {
		t.Fatalf("Tracks = %+v, %v", tracks, err)
	}
	ticks, err := c.Ticks(ctx, Date{2026, time.October, 16}, Date{2026, time.October, 18})
	if err != nil || len(ticks) != 4 {
		t.Fatalf("Ticks = %+v, %v", ticks, err)
	}
	got := map[string]string{}
	for _, tk := range ticks {
		b, _ := json.Marshal(tk.Value)
		got[tk.Date.String()+"/"+tracks[tk.TrackID-1].Name] = string(b)
	}
	want := map[string]string{"2026-10-17/Exercise": "1", "2026-10-16/Mood": "7", "2026-10-17/Mood": "7.5", "2026-10-18/Mood": `"red"`}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	notes, err := c.Notes(ctx, Date{2026, time.October, 18}, Date{2026, time.October, 18})
	if err != nil || len(notes) != 1 || notes[0].TrackID != mood {
		t.Errorf("Notes = %+v, %v", notes, err)
	}

	for i, r := range s.Logged() {
		q := s.LoggedQueries()[i]
		list := r == "GET /ocs/v2.php/apps/tickbuddy/api/tracks" || r == "GET /ocs/v2.php/apps/tickbuddy/api/ticks" || r == "GET /ocs/v2.php/apps/tickbuddy/api/notes"
		if strings.Contains(q, "includeAllTypes=true") != list {
			t.Errorf("%s?%s", r, q)
		}
	}

	// Without includeAllTypes, as from an older client, Mood is left out.
	for _, path := range []string{"/tracks", "/ticks?from=2026-10-16&to=2026-10-18", "/notes?from=2026-10-16&to=2026-10-18"} {
		req, _ := http.NewRequest(http.MethodGet, s.URL+"/ocs/v2.php/apps/tickbuddy/api"+path, nil)
		req.Header.Set("OCS-APIRequest", "true")
		req.SetBasicAuth(s.User, s.Password)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(b), `"trackId":2`) || strings.Contains(string(b), "Mood") || !strings.Contains(string(b), "Exercise") && path == "/tracks" {
			t.Errorf("%s without includeAllTypes: %s", path, b)
		}
	}
}

// TestStatsLeavesOut: stats that don't fit Stats leave out their track only.
func TestStatsLeavesOut(t *testing.T) {
	s, c, _ := setup(t)
	ex := s.AddTrack("Exercise", TypeYesNo)
	mood := s.AddTrack("Mood", "future")
	s.Stats[ex] = map[string]any{"total": 3}
	s.Stats[mood] = map[string]any{"total": 3, "maxValue": 7.5}
	stats, err := c.Stats(context.Background(), Date{2026, time.October, 18}, 0, false, false)
	if err != nil || len(stats) != 1 || stats[0].TrackID != ex {
		t.Errorf("Stats = %+v, %v", stats, err)
	}
}

func TestNeedsNewerClient(t *testing.T) {
	err := NeedsNewerClient(Track{Name: "Mood", Type: "choice"})
	var te *TypeError
	if !errors.Is(err, ErrNeedsNewerClient) || !errors.As(err, &te) || te.Track != "Mood" || te.Type != "choice" {
		t.Errorf("%#v", err)
	}
	if err.Error() != `Mood is a "choice" track; this client needs an update to tick it` {
		t.Errorf("%q", err)
	}
}
