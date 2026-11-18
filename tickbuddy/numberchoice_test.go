// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

func TestParseDecimal(t *testing.T) {
	for in, milli := range map[string]int64{
		"10.4": 10400, "-1.5": -1500, "0": 0, "72": 72000, "10.45": 10450,
		"2147483.647": 2147483647, "-2147483.647": -2147483647, "1e-3": 1, "10.400": 10400,
	} {
		d, err := ParseDecimal(in)
		if err != nil || d.Milli() != milli {
			t.Errorf("ParseDecimal(%s) = %d, %v", in, d.Milli(), err)
		}
	}
	for _, in := range []string{"10.4567", `"abc"`, "abc", "true", "", "1/2", "+5", ".5", "0x10", "1e99999"} {
		if d, err := ParseDecimal(in); err == nil {
			t.Errorf("ParseDecimal(%s) = %d", in, d.Milli())
		}
	}
}

func TestDecimalFormat(t *testing.T) {
	for _, tc := range []struct {
		milli  int64
		places int
		want   string
	}{
		{10450, 1, "10.5"}, {-10450, 1, "-10.5"}, {50, 1, "0.1"}, {72000, 1, "72.0"},
		{10400, 0, "10"}, {-40, 1, "0.0"}, {1204500, 2, "1204.50"}, {10449, 1, "10.4"},
		{-1500, 1, "-1.5"}, {0, 3, "0.000"}, {-500, 0, "-1"}, {-499, 0, "0"}, {10450, 3, "10.450"},
	} {
		if got := (Decimal{tc.milli}).Format(tc.places); got != tc.want {
			t.Errorf("%d at %d = %s, want %s", tc.milli, tc.places, got, tc.want)
		}
	}
}

// TestDecimalRoundTrip: String and MarshalJSON write every stored value back
// exactly.
func TestDecimalRoundTrip(t *testing.T) {
	for _, m := range []int64{0, 1, -1, 10, 100, 999, 1000, 10400, 10450, 72000, -1500, 2147483647, -2147483647, 123456789} {
		d := Decimal{m}
		b, _ := json.Marshal(d)
		back, err := ParseDecimal(string(b))
		if err != nil || back != d || d.String() != string(b) {
			t.Errorf("%d: %s, %d, %v", m, b, back.Milli(), err)
		}
	}
	for m, want := range map[int64]string{10400: "10.4", -1500: "-1.5", 72000: "72", 0: "0", 10450: "10.45", 1: "0.001"} {
		if got := (Decimal{m}).String(); got != want {
			t.Errorf("String(%d) = %s, want %s", m, got, want)
		}
	}
}

func TestValidNumber(t *testing.T) {
	for _, s := range []string{"10.4", "-1.5", " 7 ", "007.50", "0"} {
		if !ValidNumber(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"10,4", "+5", ".5", "5.", "1e3", "−3", "", "  ", "1 000", "--1"} {
		if ValidNumber(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestValueForNumberChoice(t *testing.T) {
	num, choice := Track{Type: TypeNumber}, Track{Type: TypeChoice}
	for _, tc := range []struct {
		t        Track
		in, want string
		dec      bool
	}{
		{num, "10.4", "10.4", true}, {num, "72", "72", true}, {num, "0", "0", true}, {num, "-1.5", "-1.5", true},
		{num, "null", "null", false}, {num, "10.4567", "10.4567", false}, {num, `"x"`, `"x"`, false},
		{choice, "2", "2", false}, {choice, "null", "null", false}, {choice, "2.5", "2.5", false},
	} {
		var v Value
		_ = json.Unmarshal([]byte(tc.in), &v)
		got := ValueFor(tc.t, v)
		b, _ := json.Marshal(got)
		if _, ok := got.Decimal(); string(b) != tc.want || ok != tc.dec {
			t.Errorf("ValueFor(%s, %s) = %s, decimal %v", tc.t.Type, tc.in, b, ok)
		}
	}
	var v Value
	_ = json.Unmarshal([]byte("10.45"), &v)
	if d, _ := ValueFor(num, v).Decimal(); d.Format(1) != "10.5" || d.String() != "10.45" {
		t.Errorf("10.45: %s, %s", d.Format(1), d)
	}
	if !(Track{Type: TypeNumber}).Known() || !(Track{Type: TypeChoice}).Known() {
		t.Error("Known")
	}
}

// numberChoice adds Run (km, 1 decimal), Low (°C, negatives) and Mood (four
// options, ordered).
func numberChoice(s *tickbuddytest.Server) (run, low, mood int) {
	run = s.AddNumber("Run", "km", 1, false)
	low = s.AddNumber("Greenhouse low", "°C", 1, true)
	mood = s.AddChoice("Mum's mood", true, "Excited", "Happy", "Normal", "Sad")
	return
}

var nov17 = Date{2026, time.November, 17}

func TestTrackSettings(t *testing.T) {
	s, c, _ := setup(t)
	numberChoice(s)
	s.AddTrack("Exercise", TypeYesNo)
	tracks, err := c.Tracks(context.Background(), false)
	if err != nil || len(tracks) != 4 {
		t.Fatalf("%+v, %v", tracks, err)
	}
	run, mood, ex := tracks[0], tracks[2], tracks[3]
	if run.Unit != "km" || run.DecimalPlaces != 1 || run.AllowNegative || !tracks[1].AllowNegative {
		t.Errorf("Run %+v", run)
	}
	if len(mood.Options) != 4 || mood.Options[1] != (ChoiceOption{2, "Happy"}) || !mood.OrderedScale {
		t.Errorf("Mood %+v", mood)
	}
	if o, ok := mood.Option(4); !ok || o.Label != "Sad" {
		t.Errorf("Option(4) = %+v", o)
	}
	if _, ok := mood.Option(5); ok {
		t.Error("Option(5)")
	}
	// Marshalled, the settings appear only on their own type.
	for tr, want := range map[*Track]string{
		&run:  `{"id":1,"name":"Run","type":"number","sortOrder":0,"private":false,"archivedAt":null,"unit":"km","decimalPlaces":1,"allowNegative":false}`,
		&mood: `{"id":3,"name":"Mum's mood","type":"choice","sortOrder":2,"private":false,"archivedAt":null,"options":[{"value":1,"label":"Excited"},{"value":2,"label":"Happy"},{"value":3,"label":"Normal"},{"value":4,"label":"Sad"}],"orderedScale":true}`,
		&ex:   `{"id":4,"name":"Exercise","type":"boolean","sortOrder":3,"private":false,"archivedAt":null}`,
	} {
		if b, _ := json.Marshal(*tr); string(b) != want {
			t.Errorf("got  %s\nwant %s", b, want)
		}
	}
	run.Unit = ""
	if b, _ := json.Marshal(run); strings.Contains(string(b), "unit") {
		t.Errorf("empty unit: %s", b)
	}
}

func TestSetNumber(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	run, low, _ := numberChoice(s)
	r, err := c.SetNumber(ctx, run, nov17, " 10.45 ", nil)
	if d, ok := r.Value.Decimal(); err != nil || !ok || d.String() != "10.5" || r.Archived {
		t.Fatalf("SetNumber = %+v, %v", r, err)
	}
	if s.Tick(run, "2026-11-17") != 10500 {
		t.Errorf("stored %d", s.Tick(run, "2026-11-17"))
	}
	if b := s.LoggedBodies("/ticks/set"); b[0] != `{"trackId":1,"date":"2026-11-17","value":"10.45"}` {
		t.Errorf("body %s", b[0])
	}
	// 0 is a reading.
	if r, err := c.SetNumber(ctx, run, nov17, "0", nil); err != nil || r.Value.String() != "0" || !s.HasTick(run, "2026-11-17") {
		t.Errorf("0: %+v, %v", r, err)
	}
	// Not a number: nothing sent.
	before := len(s.Logged())
	for _, bad := range []string{"10,4", "", "  ", "1e3"} {
		if _, err := c.SetNumber(ctx, run, nov17, bad, nil); !errors.Is(err, ErrBadNumber) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if len(s.Logged()) != before {
		t.Errorf("sent %v", s.Logged()[before:])
	}
	// The server's refusals.
	if _, err := c.SetNumber(ctx, run, nov17, "-1", nil); !errors.Is(err, ErrBadRequest) || err.Error() != "This track doesn't allow negative values" {
		t.Errorf("negative: %v", err)
	}
	if _, err := c.SetNumber(ctx, run, nov17, "99999999", nil); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "between") {
		t.Errorf("limit: %v", err)
	}
	if r, err := c.SetNumber(ctx, low, nov17, "-1.5", ReplaceNote("Frost")); err != nil || r.Value.String() != "-1.5" || *r.Note != "Frost" {
		t.Errorf("low: %+v, %v", r, err)
	}
	// Clearing.
	r, err = c.ClearValue(ctx, low, nov17, nil)
	if err != nil || !r.Value.IsNull() || s.HasTick(low, "2026-11-17") {
		t.Errorf("clear: %+v, %v", r, err)
	}
	if n, _ := s.Note(low, "2026-11-17"); n != "Frost" {
		t.Errorf("note %q", n)
	}
	if b := s.LoggedBodies("/ticks/set"); b[len(b)-1] != `{"trackId":2,"date":"2026-11-17","value":null}` {
		t.Errorf("body %s", b[len(b)-1])
	}
	// Already empty.
	if r, err := c.ClearValue(ctx, low, nov17, AppendNote("Clear")); err != nil || !r.Value.IsNull() || *r.Note != "Frost\nClear" {
		t.Errorf("clear again: %+v, %v", r, err)
	}
}

func TestSetChoice(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	_, _, mood := numberChoice(s)
	r, err := c.SetChoice(ctx, mood, nov17, 2, AppendNote("Calmer after lunch"))
	if err != nil || r.Value.Int() != 2 || r.Value.String() != "2" || *r.Note != "Calmer after lunch" || r.Archived {
		t.Fatalf("SetChoice = %+v, %v", r, err)
	}
	if b := s.LoggedBodies("/ticks/set"); b[0] != `{"trackId":3,"date":"2026-11-17","value":2,"appendNote":"Calmer after lunch"}` {
		t.Errorf("body %s", b[0])
	}
	if _, err := c.SetChoice(ctx, mood, nov17, 5, nil); !errors.Is(err, ErrBadRequest) {
		t.Errorf("place 5: %v", err)
	}
	// increment is the wrong call for a Choice.
	if _, err := c.Add(ctx, mood, nov17, 1, nil); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Add: %v", err)
	}
}

// TestSetValueRetries: setting is safe to repeat, so a 502 is retried.
func TestSetValueRetries(t *testing.T) {
	s, c, waits := setup(t)
	run, _, mood := numberChoice(s)
	s.FailNext = 2
	if r, err := c.SetNumber(context.Background(), run, nov17, "5", nil); err != nil || r.Value.String() != "5" || len(*waits) != 2 {
		t.Errorf("%+v, %v, %v", r, err, *waits)
	}
	s.FailNext = 1
	if _, err := c.ClearValue(context.Background(), mood, nov17, nil); err != nil || s.Count("POST") != 5 {
		t.Errorf("%v, %v", err, s.Logged())
	}
}

// TestSetValueConflict: a 409 after the value was written appends the note
// on its own and reads the stored, rounded value.
func TestSetValueConflict(t *testing.T) {
	s, c, _ := setup(t)
	run, _, _ := numberChoice(s)
	s.ConflictNext = 1
	r, err := c.SetNumber(context.Background(), run, nov17, "10.45", AppendNote("Park run"))
	if err != nil || r.Value.String() != "10.5" || *r.Note != "Park run" {
		t.Errorf("%+v, %v", r, err)
	}
}

// TestSetValueLocked: a track archived after it was listed answers 423.
func TestSetValueLocked(t *testing.T) {
	s, c, _ := setup(t)
	run, _, mood := numberChoice(s)
	s.Update(run, func(t *tickbuddytest.Track) { t.ReadOnly = ReasonArchived })
	s.Update(mood, func(t *tickbuddytest.Track) { t.ReadOnly = ReasonArchived })
	if _, err := c.SetNumber(context.Background(), run, nov17, "5", nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("number: %v", err)
	}
	if _, err := c.SetChoice(context.Background(), mood, nov17, 1, nil); !errors.Is(err, ErrReadOnly) {
		t.Errorf("choice: %v", err)
	}
}

func TestReadNumberChoice(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	run, low, mood := numberChoice(s)
	s.SetTick(run, "2026-11-16", 10450)
	s.SetTick(low, "2026-11-16", 0)
	s.SetTick(low, "2026-11-17", -1500)
	s.SetTick(mood, "2026-11-17", 2)
	tracks, _ := c.Tracks(ctx, false)
	byID := map[int]Track{}
	for _, tr := range tracks {
		byID[tr.ID] = tr
	}
	ticks, err := c.Ticks(ctx, nov17.AddDays(-1), nov17)
	if err != nil || len(ticks) != 4 {
		t.Fatalf("%+v, %v", ticks, err)
	}
	got := map[string]string{}
	for _, tk := range ticks {
		got[tk.Date.String()+"/"+byID[tk.TrackID].Name] = ValueFor(byID[tk.TrackID], tk.Value).String()
	}
	for k, v := range map[string]string{"2026-11-16/Run": "10.45", "2026-11-16/Greenhouse low": "0", "2026-11-17/Greenhouse low": "-1.5", "2026-11-17/Mum's mood": "2"} {
		if got[k] != v {
			t.Errorf("%s = %q, want %s", k, got[k], v)
		}
	}
}

func TestResolveOption(t *testing.T) {
	mood := Track{Name: "Mum's mood", Type: TypeChoice, Options: []ChoiceOption{{1, "Excited"}, {2, "Happy"}, {3, "Normal"}, {4, "Sad"}, {5, "Nervous"}, {6, "#7"}}}
	pain := Track{Name: "Pain", Type: TypeChoice, Options: []ChoiceOption{{1, "0"}, {2, "1"}, {3, "2"}}}
	for _, tc := range []struct {
		t           Track
		q           string
		interactive bool
		place       int
	}{
		{mood, "#2", false, 2}, {mood, " #4 ", false, 4}, {mood, "happy", false, 2}, {mood, "  HAPPY ", false, 2},
		{mood, "hap", true, 2}, {mood, "#6", false, 6}, {mood, "#7", false, 0},
		{pain, "2", false, 3}, {pain, "#2", false, 2},
	} {
		o, err := ResolveOption(tc.t, tc.q, tc.interactive)
		if tc.place == 0 {
			if !errors.Is(err, ErrOptionNotFound) {
				t.Errorf("%q: %+v, %v", tc.q, o, err)
			}
			continue
		}
		if err != nil || o.Value != tc.place {
			t.Errorf("%q: %+v, %v", tc.q, o, err)
		}
	}
	_, err := ResolveOption(mood, "hap", false)
	var oe *OptionError
	if !errors.Is(err, ErrOptionNotFound) || !errors.As(err, &oe) || len(oe.Candidates) != 6 {
		t.Errorf("prefix, not interactive: %v", err)
	}
	if _, err := ResolveOption(mood, "#0", false); !errors.Is(err, ErrOptionNotFound) {
		t.Errorf("#0: %v", err)
	}
	_, err = ResolveOption(mood, "hapy", false)
	if err == nil || err.Error() != `Mum's mood has no option "hapy". Options: Excited (#1), Happy (#2), Normal (#3), Sad (#4), Nervous (#5), #7 (#6)` {
		t.Errorf("not found: %v", err)
	}
	_, err = ResolveOption(mood, "N", true)
	if !errors.Is(err, ErrOptionAmbiguous) || err.Error() != `"N" matches several options of Mum's mood: Normal (#3), Nervous (#5)` {
		t.Errorf("ambiguous: %v", err)
	}
	// Two labels equal ignoring case: ambiguous, and #N reaches either.
	twin := Track{Name: "Twin", Options: []ChoiceOption{{1, "Straße"}, {2, "STRASSE"}, {3, "straße"}}}
	if _, err := ResolveOption(twin, "straße", false); !errors.Is(err, ErrOptionAmbiguous) {
		t.Errorf("twin: %v", err)
	}
	if o, err := ResolveOption(twin, "#3", false); err != nil || o.Label != "straße" {
		t.Errorf("twin #3: %+v, %v", o, err)
	}
}

func TestCreateTrackSettings(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	unit, places, neg, ordered := "km", 2, true, true
	tr, err := c.CreateTrack(ctx, "Run", TypeNumber, &TrackSettings{Unit: &unit, DecimalPlaces: &places, AllowNegative: &neg})
	if err != nil || tr.Unit != "km" || tr.DecimalPlaces != 2 || !tr.AllowNegative || tr.Type != TypeNumber {
		t.Errorf("number: %+v, %v", tr, err)
	}
	tr, err = c.CreateTrack(ctx, "Mood", TypeChoice, &TrackSettings{Options: []string{"Happy", "Sad"}, OrderedScale: &ordered})
	if err != nil || len(tr.Options) != 2 || tr.Options[1].Label != "Sad" || !tr.OrderedScale {
		t.Errorf("choice: %+v, %v", tr, err)
	}
	bodies := s.LoggedBodies("/tracks")
	if bodies[0] != `{"name":"Run","type":"number","unit":"km","decimalPlaces":2,"allowNegative":true}` ||
		bodies[1] != `{"name":"Mood","type":"choice","options":["Happy","Sad"],"orderedScale":true}` {
		t.Errorf("bodies %q", bodies)
	}
	// The server's checks.
	if _, err := c.CreateTrack(ctx, "Mood", TypeChoice, &TrackSettings{Options: []string{"Only"}}); !errors.Is(err, ErrBadRequest) || !strings.Contains(err.Error(), "2 to 99") {
		t.Errorf("one option: %v", err)
	}
	if _, err := c.CreateTrack(ctx, "Run", TypeNumber, &TrackSettings{Options: []string{"a", "b"}}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("options on a number: %v", err)
	}
	if _, err := c.CreateTrack(ctx, "Plain", TypeNumber, nil); err != nil || s.LoggedBodies("/tracks")[4] != `{"name":"Plain","type":"number"}` {
		t.Errorf("nil settings: %v", err)
	}

	// Without the flags: nothing sent.
	s, c, _ = setup(t)
	s.Features[FeatureNumberTracks], s.Features[FeatureChoiceTracks] = false, false
	if _, err := c.CreateTrack(ctx, "Run", TypeNumber, nil); !errors.Is(err, ErrNeedsNewerServer) || !strings.Contains(err.Error(), "numberTracks") {
		t.Errorf("number: %v", err)
	}
	if _, err := c.CreateTrack(ctx, "Mood", TypeChoice, nil); !errors.Is(err, ErrNeedsNewerServer) || !strings.Contains(err.Error(), "choiceTracks") {
		t.Errorf("choice: %v", err)
	}
	if _, err := c.RenameOption(ctx, 1, 1, "x"); !errors.Is(err, ErrNeedsNewerServer) {
		t.Errorf("rename: %v", err)
	}
	if n := s.Count("POST"); n != 0 {
		t.Errorf("sent %v", s.Logged())
	}
	if _, err := c.CreateTrack(ctx, "Exercise", TypeYesNo, nil); err != nil {
		t.Errorf("yes/no: %v", err)
	}
}

func TestRenameOption(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	run, _, mood := numberChoice(s)
	tr, err := c.RenameOption(ctx, mood, 2, "Cheerful")
	if err != nil || tr.Options[1].Label != "Cheerful" || tr.Name != "Mum's mood" {
		t.Errorf("%+v, %v", tr, err)
	}
	if l := s.Logged(); l[len(l)-1] != "PUT /ocs/v2.php/apps/tickbuddy/api/tracks/3/options/2" {
		t.Errorf("%v", l)
	}
	_, err = c.RenameOption(ctx, mood, 9, "x")
	if !errors.Is(err, ErrOptionNotFound) || errors.Is(err, ErrTrackNotFound) {
		t.Errorf("place 9: %v", err)
	}
	if _, err := c.RenameOption(ctx, 99, 1, "x"); !errors.Is(err, ErrTrackNotFound) {
		t.Errorf("track 99: %v", err)
	}
	if _, err := c.RenameOption(ctx, run, 1, "x"); !errors.Is(err, ErrBadRequest) {
		t.Errorf("a number: %v", err)
	}
	s.Update(mood, func(t *tickbuddytest.Track) { t.ArchivedAt = "2026-11-01" })
	if _, err := c.RenameOption(ctx, mood, 1, "x"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("archived: %v", err)
	}
}

func TestStatsNumberChoice(t *testing.T) {
	s, c, _ := setup(t)
	run, low, mood := numberChoice(s)
	plain := s.AddChoice("Morning exercise", false, "Run", "Swim")
	s.SetTick(run, "2026-11-16", 10450)
	s.SetTick(run, "2026-11-17", 5000)
	s.SetTick(low, "2026-11-17", -1500)
	s.SetTick(mood, "2026-11-17", 2)
	s.SetTick(plain, "2026-11-17", 1)
	stats, err := c.Stats(context.Background(), nov17, 0, false, false)
	if err != nil || len(stats) != 4 {
		t.Fatalf("%+v, %v", stats, err)
	}
	for i, want := range [][2]string{{"10.45", "5"}, {"-1.5", "-1.5"}, {"2", "2"}, {"null", "null"}} {
		mx, _ := json.Marshal(stats[i].MaxValue)
		mn, _ := json.Marshal(stats[i].MinValue)
		if string(mx) != want[0] || string(mn) != want[1] {
			t.Errorf("track %d: max %s, min %s", stats[i].TrackID, mx, mn)
		}
	}
	if d, ok := ValueFor(Track{Type: TypeNumber}, stats[0].MaxValue).Decimal(); !ok || d.Milli() != 10450 {
		t.Errorf("max as a decimal: %v", d)
	}
	if stats[0].Total != 2 {
		t.Errorf("total %d", stats[0].Total)
	}
	if q := s.StatsQueries[0]; q.Get("includeAllTypes") != "true" {
		t.Errorf("query %v", q)
	}
	if _, err := c.StatsPeriod(context.Background(), nov17, nov17, nov17, 0, false, false); err != nil || s.StatsQueries[1].Get("includeAllTypes") != "true" {
		t.Errorf("period: %v, %v", err, s.StatsQueries[1])
	}
}

// TestIncludeAllTypesFake: without includeAllTypes the fake leaves Number and
// Choice tracks out of every read, as the server does, so the tests above
// fail if the package stops sending it.
func TestIncludeAllTypesFake(t *testing.T) {
	s, _, _ := setup(t)
	run, _, mood := numberChoice(s)
	s.SetTick(run, "2026-11-17", 5000)
	s.SetTick(mood, "2026-11-17", 2)
	s.SetNote(run, "2026-11-17", "Park")
	for _, path := range []string{"/tracks", "/ticks?from=2026-11-17&to=2026-11-17", "/notes?from=2026-11-17&to=2026-11-17", "/stats?today=2026-11-17"} {
		req, _ := http.NewRequest(http.MethodGet, s.URL+"/ocs/v2.php/apps/tickbuddy/api"+path, nil)
		req.Header.Set("OCS-APIRequest", "true")
		req.SetBasicAuth(s.User, s.Password)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			OCS struct {
				Data []any `json:"data"`
			} `json:"ocs"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&env)
		resp.Body.Close()
		if len(env.OCS.Data) != 0 {
			t.Errorf("%s without includeAllTypes: %v", path, env.OCS.Data)
		}
	}
}

func TestMissingNewFlags(t *testing.T) {
	c := Capabilities{Features: map[string]bool{}}
	m := strings.Join(c.Missing(), ",")
	if !strings.HasSuffix(m, "notesAppend,numberTracks,choiceTracks") {
		t.Errorf("Missing = %s", m)
	}
}
