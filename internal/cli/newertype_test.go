// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

// future adds Mood, a track of a type Tickcli doesn't know, with a value of
// each kind on 16, 17 and 18 Oct and a note on the 17th.
func (e *env) future() *env {
	s := e.srv
	e.ids["mood"] = s.AddTrack("Mood", "future")
	s.SetValue(e.ids["mood"], "2026-10-16", 7)
	s.SetValue(e.ids["mood"], "2026-10-17", 7.5)
	s.SetValue(e.ids["mood"], "2026-10-18", "red")
	s.SetNote(e.ids["mood"], "2026-10-17", "Fine-ish")
	return e
}

// TestNewerTypeGolden: the lists show Mood read-only beside every other
// track (decision 29), in all three output modes.
func TestNewerTypeGolden(t *testing.T) {
	cmds := map[string][]string{
		"future-today":     {},
		"future-ticks-all": {"ticks", "--from", "-2"},
		"future-ticks-one": {"ticks", "Mood", "--from", "-3"},
		"future-week":      {"week"},
		"future-tracks":    {"tracks"},
	}
	others := []string{"Exercise", "Made someone smile", "Cups of coffee", "Nightly backup"}
	for name, args := range cmds {
		for _, m := range []string{"pretty", "plain", "json"} {
			t.Run(name+"-"+m, func(t *testing.T) {
				e := newEnv(t).seed().future()
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
				if !strings.Contains(r.stdout, "Mood") {
					t.Errorf("no Mood:\n%s", r.stdout)
				}
				if name != "future-ticks-one" {
					for _, o := range others {
						if !strings.Contains(r.stdout, o) {
							t.Errorf("no %s:\n%s", o, r.stdout)
						}
					}
				}
				label := strings.Contains(r.stdout, "(Tickcli update required)")
				if label != (m == "pretty" && name != "future-tracks") {
					t.Errorf("label %v:\n%s", label, r.stdout)
				}
				golden(t, name+"-"+m, r.stdout)
			})
		}
	}
}

// TestNewerTypeFiltered: --private and --archived filter Mood as any track.
func TestNewerTypeFiltered(t *testing.T) {
	e := newEnv(t).seed().future()
	e.srv.Update(e.ids["mood"], func(t *tickbuddytest.Track) { t.Private = true })
	if r := e.run(false, "", "tracks"); r.code != 0 || strings.Contains(r.stdout, "Mood") {
		t.Errorf("private Mood listed: %+v", r)
	}
	if r := e.run(false, "", "tracks", "--private"); r.code != 0 || !strings.Contains(r.stdout, "Mood\tfuture\tprivate") {
		t.Errorf("--private: %+v", r)
	}
}

// TestNewerTypeWrites: tick, untick, set and add are refused before sending
// (decision 30), a note with them too; note works (decision 31).
func TestNewerTypeWrites(t *testing.T) {
	const msg = `Mood is a "future" track; Tickcli update required`
	for _, args := range [][]string{
		{"tick", "Mood"}, {"untick", "Mood"}, {"set", "Mood", "3"}, {"add", "Mood"},
		{"add", "Mood", "1", "--note", "x"}, {"tick", "Mood", "--append-note", "x"},
	} {
		e := newEnv(t).seed().future()
		before := len(e.srv.Logged())
		r := e.run(false, "", args...)
		if r.code != 2 || r.stderr != "tickcli: "+msg+"\n" || r.stdout != "" {
			t.Errorf("%v: %+v", args, r)
		}
		r = e.run(false, "", append(args, "--json")...)
		var got struct {
			Error string `json:"error"`
			Code  int    `json:"code"`
		}
		if err := json.Unmarshal([]byte(r.stderr), &got); err != nil || r.code != 2 || got.Code != 2 || got.Error != msg {
			t.Errorf("%v --json: %+v", args, r)
		}
		for _, l := range e.srv.Logged()[before:] {
			if !strings.HasPrefix(l, "GET ") {
				t.Errorf("%v sent %s", args, l)
			}
		}
		if n, _ := e.srv.Note(e.ids["mood"], "2026-10-18"); n != "" {
			t.Errorf("%v: note %q", args, n)
		}
	}

	e := newEnv(t).seed().future()
	id := e.ids["mood"]
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"note", "Mood", "Sunny"}, "Sunny"},
		{[]string{"note", "Mood", "Rain later", "--append"}, "Sunny\nRain later"},
		{[]string{"note", "Mood", "--clear"}, ""},
	} {
		if r := e.run(false, "", tc.args...); r.code != 0 {
			t.Errorf("%v: %+v", tc.args, r)
		}
		if n, _ := e.srv.Note(id, "2026-10-18"); n != tc.want {
			t.Errorf("%v: note %q", tc.args, n)
		}
	}
	if r := e.run(false, "", "notes"); r.code != 0 || r.stdout != "2026-10-18\tNightly backup\tFinished 02:14, 3.2 GB\n2026-10-17\tMood\tFine-ish\n" {
		t.Errorf("notes: %+v", r)
	}
}

// TestNewerTypeStats: stats shows what the server sends for Mood, and leaves
// it out when that doesn't fit.
func TestNewerTypeStats(t *testing.T) {
	e := newEnv(t).seed().future()
	e.srv.Stats[e.ids["mood"]] = map[string]any{"total": 3}
	if r := e.run(false, "", "stats"); r.code != 0 || !strings.Contains(r.stdout, "Mood\t3\t") || !strings.Contains(r.stdout, "Exercise\t212") {
		t.Errorf("stats: %+v", r)
	}
	if r := e.run(false, "", "stats", "Mood"); r.code != 0 || !strings.Contains(r.stdout, "total\t3\n") {
		t.Errorf("stats Mood: %+v", r)
	}
	e.srv.Stats[e.ids["mood"]] = map[string]any{"total": 3, "maxValue": 7.5}
	if r := e.run(false, "", "stats"); r.code != 0 || strings.Contains(r.stdout, "Mood") || !strings.Contains(r.stdout, "Exercise\t212") {
		t.Errorf("stats, Mood's unreadable: %+v", r)
	}
	if r := e.run(true, "", "week"); r.code != 0 || !strings.Contains(r.stdout, "Mood") {
		t.Errorf("week, Mood's stats unreadable: %+v", r)
	}
}

// TestIncludeAllTypes: sent on GET /tracks, /ticks and /notes, on every
// call, and nowhere else (decision 32).
func TestIncludeAllTypes(t *testing.T) {
	e := newEnv(t).seed().future()
	for _, args := range [][]string{
		{}, {"tracks"}, {"ticks", "Mood"}, {"week"}, {"notes"}, {"stats"}, {"stats", "Exercise", "--year", "2025"},
		{"export"}, {"tick", "Made someone smile"}, {"add", "Cups of coffee"}, {"note", "Mood", "x"}, {"status"},
	} {
		if r := e.run(false, "", args...); r.code != 0 {
			t.Fatalf("%v: %+v", args, r)
		}
	}
	lists := map[string]bool{}
	for i, r := range e.srv.Logged() {
		q := e.srv.LoggedQueries()[i]
		list := strings.HasPrefix(r, "GET ") && (strings.HasSuffix(r, "/api/tracks") || strings.HasSuffix(r, "/api/ticks") || strings.HasSuffix(r, "/api/notes"))
		if list {
			lists[r] = true
		}
		if strings.Contains(q, "includeAllTypes=true") != list {
			t.Errorf("%s?%s", r, q)
		}
	}
	if len(lists) != 3 {
		t.Errorf("lists called: %v", lists)
	}
}

// TestNewerTypeExport: the export passes Mood through unchanged.
func TestNewerTypeExport(t *testing.T) {
	e := newEnv(t).seed().future()
	r := e.run(false, "", "export")
	var got struct {
		Tracks []map[string]any `json:"tracks"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &got); r.code != 0 || err != nil {
		t.Fatalf("%+v, %v", r, err)
	}
	found := false
	for _, tr := range got.Tracks {
		found = found || tr["name"] == "Mood" && tr["type"] == "future"
	}
	if !found {
		t.Errorf("no Mood in %s", r.stdout)
	}
}
