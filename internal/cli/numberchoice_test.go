// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

// numbers adds the Number and Choice fixture tracks after seed's: Run (km, 1
// decimal, a day stored as 10.45 and one as 72), Greenhouse low (°C,
// negatives, -1.5 and 0), Weight (no unit, 2 decimals), Mum's mood (an
// ordered scale), Morning exercise (a plain list) and Pain (labels "0", "1",
// "2").
func (e *env) numbers() *env {
	s := e.srv
	e.ids["run"] = s.AddNumber("Run", "km", 1, false)
	e.ids["low"] = s.AddNumber("Greenhouse low", "°C", 1, true)
	e.ids["weight"] = s.AddNumber("Weight", "", 2, false)
	e.ids["mood"] = s.AddChoice("Mum's mood", true, "Excited", "Happy", "Normal", "Sad")
	e.ids["morning"] = s.AddChoice("Morning exercise", false, "Run", "Swim", "Yoga")
	e.ids["pain"] = s.AddChoice("Pain", false, "0", "1", "2")
	s.SetTick(e.ids["run"], "2026-10-18", 10450)
	s.SetTick(e.ids["run"], "2026-10-16", 72000)
	s.SetTick(e.ids["run"], "2026-10-14", 5000)
	s.SetTick(e.ids["low"], "2026-10-18", -1500)
	s.SetTick(e.ids["low"], "2026-10-17", 0)
	s.SetTick(e.ids["weight"], "2026-10-18", 72500)
	s.SetTick(e.ids["mood"], "2026-10-18", 2)
	s.SetTick(e.ids["mood"], "2026-10-17", 3)
	s.SetTick(e.ids["mood"], "2026-10-15", 1)
	s.SetNote(e.ids["mood"], "2026-10-18", "Calmer after lunch")
	s.SetTick(e.ids["morning"], "2026-10-18", 3)
	s.SetTick(e.ids["pain"], "2026-10-18", 3)
	return e
}

// TestNumberChoiceGolden: every reading command in all three modes, and the
// value commands with --numeric.
func TestNumberChoiceGolden(t *testing.T) {
	cmds := map[string][]string{
		"nc-today":           {},
		"nc-today-numeric":   {"--numeric"},
		"nc-ticks-one":       {"ticks", "Run", "--from", "-4"},
		"nc-ticks-mood":      {"ticks", "Mum's mood", "--from", "-3", "--numeric"},
		"nc-ticks-all":       {"ticks", "--from", "-1"},
		"nc-week":            {"week"},
		"nc-week-numeric":    {"week", "--numeric"},
		"nc-tracks":          {"tracks"},
		"nc-stats":           {"stats"},
		"nc-stats-run":       {"stats", "Run"},
		"nc-ticks-all-numer": {"ticks", "--numeric"},
	}
	for name, args := range cmds {
		for _, m := range []string{"pretty", "plain", "json"} {
			t.Run(name+"-"+m, func(t *testing.T) {
				e := newEnv(t).seed().numbers().future()
				a := args
				if m == "json" {
					a = append(append([]string{}, args...), "--json")
				}
				r := e.run(m == "pretty", "", a...)
				if r.code != 0 {
					t.Fatalf("exit %d: %s", r.code, r.stderr)
				}
				if m == "json" && !json.Valid([]byte(r.stdout)) {
					t.Errorf("invalid JSON: %s", r.stdout)
				}
				if strings.Contains(r.stdout, "update required") && strings.Contains(r.stdout, "Run  (Tickcli") {
					t.Errorf("Run read-only:\n%s", r.stdout)
				}
				golden(t, name+"-"+m, r.stdout)
			})
		}
	}
}

func TestNumberChoiceValues(t *testing.T) {
	e := newEnv(t).seed().numbers().future()
	// Plain: the value at the track's decimal places, no unit; a label; an
	// empty field for no value, the tab still written.
	r := e.run(false, "", "ticks", "--from", "-1")
	for _, want := range []string{
		"2026-10-18\tRun\t10.5\n", "2026-10-17\tRun\t\n", "2026-10-18\tGreenhouse low\t-1.5\n", "2026-10-17\tGreenhouse low\t0.0\n",
		"2026-10-18\tWeight\t72.50\n", "2026-10-18\tMum's mood\tHappy\tCalmer after lunch\n", "2026-10-18\tPain\t2\n",
		"2026-10-17\tMood\t7.5\tFine-ish\n", "2026-10-18\tMood\t\"red\"\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("no %q in\n%s", want, r.stdout)
		}
	}
	r = e.run(false, "", "ticks", "--from", "-1", "--numeric")
	for _, want := range []string{"2026-10-18\tExercise\t1\n", "2026-10-18\tMade someone smile\t0\n", "2026-10-18\tMum's mood\t2\t", "2026-10-18\tPain\t3\n", "2026-10-18\tRun\t10.5\n", "2026-10-17\tRun\t\n"} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("--numeric: no %q in\n%s", want, r.stdout)
		}
	}
	// JSON: the stored value exactly, with unit and label.
	r = e.run(false, "", "ticks", "Run", "--json")
	if !strings.Contains(r.stdout, `{"date":"2026-10-18","value":10.45,"unit":"km"}`) {
		t.Errorf("Run --json: %s", r.stdout)
	}
	r = e.run(false, "", "ticks", "Run", "-d", "-1", "--json")
	if !strings.Contains(r.stdout, `{"date":"2026-10-17","value":null,"unit":"km"}`) {
		t.Errorf("empty Run --json: %s", r.stdout)
	}
	r = e.run(false, "", "--json", "--numeric")
	if !strings.Contains(r.stdout, `"value":2,"label":"Happy"`) || !strings.Contains(r.stdout, `"value":true`) {
		t.Errorf("today --json --numeric: %s", r.stdout)
	}
}

// TestWeekWidth: week widens only for the Number and Choice rows shown.
func TestWeekWidth(t *testing.T) {
	e := newEnv(t).seed()
	run := e.srv.AddNumber("Run", "km", 1, false)
	e.srv.SetTick(run, "2026-10-18", 1234567)
	e.srv.Update(run, func(t *tickbuddytest.Track) { t.Private = true })
	narrow := e.run(true, "", "week")
	plain := newEnv(t).seed().run(true, "", "week")
	if narrow.stdout != plain.stdout {
		t.Errorf("a private Number widened week:\n%s\nwant\n%s", narrow.stdout, plain.stdout)
	}
	wide := e.run(true, "", "week", "--private")
	if !strings.Contains(wide.stdout, "Run (km)            1234.6") || !strings.Contains(wide.stdout, "    Su     Sa") {
		t.Errorf("--private:\n%s", wide.stdout)
	}
	// An archived Number isn't shown, so it doesn't widen either.
	e.srv.Update(run, func(t *tickbuddytest.Track) { t.Private, t.ArchivedAt = false, "2026-10-01" })
	if r := e.run(true, "", "week"); r.stdout != plain.stdout {
		t.Errorf("an archived Number widened week:\n%s", r.stdout)
	}
	// With --numeric, a Choice alone doesn't widen it.
	e = newEnv(t).seed()
	mood := e.srv.AddChoice("Mood", false, "Happy", "Sad")
	e.srv.SetTick(mood, "2026-10-18", 1)
	if r := e.run(true, "", "week", "--numeric"); !strings.Contains(r.stdout, "Mood                1  ·  ·") {
		t.Errorf("--numeric:\n%s", r.stdout)
	}
	if r := e.run(true, "", "week"); !strings.Contains(r.stdout, "Mood                 Happy      ·") {
		t.Errorf("labels:\n%s", r.stdout)
	}
}

func TestSetNumberChoice(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		tty    bool
		code   int
		stdout string
		stderr string
		check  func(e *env) bool
	}{
		{"number", []string{"set", "Run", "10.4"}, true, 0, "Run: 10.4 km · Sun 18 Oct\n", "", func(e *env) bool { return e.srv.Tick(e.ids["run"], "2026-10-18") == 10400 }},
		{"rounded", []string{"set", "Run", "10.46"}, true, 0, "Run: 10.5 km · Sun 18 Oct\n", "", func(e *env) bool { return e.srv.Tick(e.ids["run"], "2026-10-18") == 10500 }},
		{"no unit", []string{"set", "Weight", "72"}, true, 0, "Weight: 72.00 · Sun 18 Oct\n", "", nil},
		{"negative", []string{"set", "Greenhouse low", "-1.5"}, true, 0, "Greenhouse low: -1.5 °C · Sun 18 Oct\n", "", nil},
		{"negative quoted", []string{"set", "Greenhouse low", "-2.25", "-d", "-1"}, false, 0, "", "", func(e *env) bool { return e.srv.Tick(e.ids["low"], "2026-10-17") == -2300 }},
		{"negative date first", []string{"set", "-d", "-1", "Greenhouse low", "-1"}, false, 0, "", "", func(e *env) bool { return e.srv.Tick(e.ids["low"], "2026-10-17") == -1000 }},
		{"zero", []string{"set", "Run", "0"}, false, 0, "", "", func(e *env) bool { return e.srv.HasTick(e.ids["run"], "2026-10-18") }},
		{"clear", []string{"set", "Run", ""}, true, 0, "Run: no value · Sun 18 Oct\n", "", func(e *env) bool { return !e.srv.HasTick(e.ids["run"], "2026-10-18") }},
		{"clear spaces", []string{"set", "Mum's mood", "  "}, false, 0, "", "", func(e *env) bool { return !e.srv.HasTick(e.ids["mood"], "2026-10-18") }},
		{"clear with note", []string{"set", "Run", "", "--note", "Rest day"}, true, 0, "Run: no value · Sun 18 Oct  📝 Rest day\n", "", func(e *env) bool {
			n, _ := e.srv.Note(e.ids["run"], "2026-10-18")
			return n == "Rest day" && !e.srv.HasTick(e.ids["run"], "2026-10-18")
		}},
		{"bad number", []string{"set", "Run", "10,4"}, false, 2, "", `tickcli: Run takes a number with a point, such as 10.4, or "" to clear the day` + "\n", nil},
		{"server refuses negative", []string{"set", "Run", "-1"}, false, 1, "", "tickcli: This track doesn't allow negative values\n", nil},
		{"server refuses limit", []string{"set", "Run", "99999999"}, false, 1, "", "tickcli: Numbers must be between -2147483.647 and 2147483.647\n", nil},
		{"label", []string{"set", "Mum's mood", "sad", "--append-note", "Tired"}, true, 0, "Mum's mood: Sad · Sun 18 Oct  📝 Calmer after lunch\n    Tired\n", "", func(e *env) bool { return e.srv.Tick(e.ids["mood"], "2026-10-18") == 4 }},
		{"place", []string{"set", "Mum's mood", "#3"}, true, 0, "Mum's mood: Normal · Sun 18 Oct\n", "", nil},
		{"place numeric", []string{"set", "Mum's mood", "#3", "--numeric"}, true, 0, "Mum's mood: 3 · Sun 18 Oct\n", "", nil},
		{"bare 2 is a label", []string{"set", "Pain", "2", "-d", "-1"}, false, 0, "", "", func(e *env) bool { return e.srv.Tick(e.ids["pain"], "2026-10-17") == 3 }},
		{"no option", []string{"set", "Mum's mood", "hapy"}, false, 2, "", `tickcli: Mum's mood has no option "hapy". Options: Excited (#1), Happy (#2), Normal (#3), Sad (#4)` + "\n", nil},
		{"place out of range", []string{"set", "Mum's mood", "#5"}, false, 2, "", `tickcli: Mum's mood has no option "#5". Options: Excited (#1), Happy (#2), Normal (#3), Sad (#4)` + "\n", nil},
		{"prefix in a script", []string{"set", "Mum's mood", "hap"}, false, 2, "", `has no option "hap"`, nil},
		{"prefix at a terminal", []string{"set", "Mum's mood", "hap"}, true, 0, "Mum's mood: Happy · Sun 18 Oct\n", "", nil},
		{"ambiguous prefix", []string{"set", "Morning exercise", "", "-d", "-1"}, true, 0, "Morning exercise: no value · Sat 17 Oct\n", "", nil},
		{"tick a number", []string{"tick", "Run"}, false, 2, "", "tickcli: Run is a number; use `tickcli set`\n", nil},
		{"untick a choice", []string{"untick", "Mum's mood"}, false, 2, "", "tickcli: Mum's mood is a choice; use `tickcli set`\n", nil},
		{"add to a number", []string{"add", "Run", "2.5"}, false, 2, "", "tickcli: Run is a number; use `tickcli set`\n", nil},
		{"add to a choice", []string{"add", "Mum's mood"}, false, 2, "", "tickcli: Mum's mood is a choice; use `tickcli set`\n", nil},
		{"counter empty", []string{"set", "Cups of coffee", ""}, false, 2, "", `tickcli: "" isn't a count; use a whole number, 0 or more` + "\n", nil},
		{"value first", []string{"set", "10.4", "Run"}, false, 2, "", "tickcli: the track comes first: tickcli set <track> <value>\n", nil},
		{"json number", []string{"set", "Run", "10.46", "--json"}, false, 0, `{"track":"Run","trackId":7,"date":"2026-10-18","value":10.5,"unit":"km"}` + "\n", "", nil},
		{"json choice", []string{"set", "Mum's mood", "happy", "--json", "--numeric"}, false, 0, `{"track":"Mum's mood","trackId":10,"date":"2026-10-18","value":2,"label":"Happy"}` + "\n", "", nil},
		{"json clear", []string{"set", "Mum's mood", "", "--json", "--note", ""}, false, 0, `{"track":"Mum's mood","trackId":10,"date":"2026-10-18","value":null,"note":""}` + "\n", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t).seed().numbers()
			before := e.srv.Count("POST")
			r := e.run(tc.tty, "", tc.args...)
			if r.code != tc.code || r.stdout != tc.stdout || !strings.Contains(r.stderr, tc.stderr) {
				t.Errorf("got %d %q %q; want %d %q %q", r.code, r.stdout, r.stderr, tc.code, tc.stdout, tc.stderr)
			}
			if tc.code == 2 && e.srv.Count("POST") != before {
				t.Errorf("sent despite failing: %v", e.srv.Logged())
			}
			if tc.check != nil && !tc.check(e) {
				t.Errorf("not stored as expected")
			}
		})
	}
}

// TestSetNumberArchived: refused before sending; a 423 from a track archived
// meanwhile is exit 6; a rounded answer is not taken as archived.
func TestSetNumberArchived(t *testing.T) {
	e := newEnv(t).seed().numbers()
	e.srv.Update(e.ids["run"], func(t *tickbuddytest.Track) { t.ArchivedAt = "2026-10-01" })
	if r := e.run(false, "", "set", "Run", "5"); r.code != 6 || e.srv.Count("POST") != 0 || !strings.Contains(r.stderr, `Track "Run" is archived`) {
		t.Errorf("archived: %+v", r)
	}
	e = newEnv(t).seed().numbers()
	e.srv.Update(e.ids["mood"], func(t *tickbuddytest.Track) { t.ReadOnly = "archived" })
	if r := e.run(false, "", "set", "Mum's mood", "sad"); r.code != 6 || !strings.Contains(r.stderr, `Track "Mum's mood" is archived`) {
		t.Errorf("423: %+v", r)
	}
	if r := e.run(false, "", "set", "Run", "10.46"); r.code != 0 {
		t.Errorf("rounded: %+v", r)
	}
}

func TestNumberChoiceStatus(t *testing.T) {
	e := newEnv(t).seed()
	e.srv.Features["numberTracks"], e.srv.Features["choiceTracks"] = false, false
	r := e.run(false, "", "status")
	if r.code != 0 || !strings.Contains(r.stdout, "numberTracks") || !strings.Contains(r.stdout, "choiceTracks") {
		t.Errorf("%+v", r)
	}
}

// TestEmptyDayJSON: with --json, a day without a value on a Number, Choice
// or newer type's track is "value": null (never left out, never the string
// "null"), in every command that shows a day, and after clearing one.
func TestEmptyDayJSON(t *testing.T) {
	e := newEnv(t).seed().numbers().future()
	var out strings.Builder
	for _, args := range [][]string{
		{"-d", "2026-10-10", "--json"},
		{"ticks", "Run", "-d", "2026-10-10", "--json"},
		{"ticks", "Mum's mood", "-d", "2026-10-10", "--json"},
		{"ticks", "Mood", "-d", "2026-10-10", "--json"},
		{"set", "Run", "", "--json"},
		{"set", "Mum's mood", "", "--json"},
	} {
		r := e.run(false, "", args...)
		if r.code != 0 {
			t.Fatalf("%v: %+v", args, r)
		}
		for _, name := range []string{"Run", "Greenhouse low", "Weight", "Mum's mood", "Morning exercise", "Pain", "Mood"} {
			if strings.Contains(r.stdout, `"track":"`+name+`"`) && strings.Contains(r.stdout, `"value":"null"`) {
				t.Errorf("%v: %s's null is a string: %s", args, name, r.stdout)
			}
		}
		if !strings.Contains(r.stdout, `"value":null`) {
			t.Errorf("%v: no null: %s", args, r.stdout)
		}
		shown := make([]string, len(args))
		for i, a := range args {
			shown[i] = a
			if a == "" || strings.ContainsAny(a, " '") {
				shown[i] = `"` + a + `"`
			}
		}
		out.WriteString("$ tickcli " + strings.Join(shown, " ") + "\n" + r.stdout)
	}
	golden(t, "empty-day-json", out.String())
}
