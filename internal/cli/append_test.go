// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/martinhammer/tickcli/internal/fakeserver"
)

// The Strava recipe: running it again for the same activity changes nothing.
func TestAppendRecipe(t *testing.T) {
	e := newEnv(t).seed()
	for _, text := range []string{"a", "a", "b"} {
		if r := e.run(false, "", "tick", "Exercise", "--append-note", text); r.code != 0 || r.stdout != "" {
			t.Fatalf("%s: %+v", text, r)
		}
	}
	if n, _ := e.srv.Note(e.ids["ex"], "2026-10-18"); n != "a\nb" {
		t.Errorf("note = %q", n)
	}
}

// --append-note "x" and --note "x" --append send the same request.
func TestAppendForms(t *testing.T) {
	cases := []struct {
		args   []string
		suffix string
	}{
		{[]string{"tick", "Made someone smile"}, "/ticks/set"},
		{[]string{"untick", "Exercise"}, "/ticks/set"},
		{[]string{"set", "Cups of coffee", "4"}, "/ticks/set"},
		{[]string{"add", "Cups of coffee", "2"}, "/ticks/increment"},
	}
	for _, tc := range cases {
		var sent []map[string]any
		for _, form := range [][]string{
			{"--append-note", "x"}, {"--note", "x", "--append"},
			{"--append-note", "x", "--force-duplicate"}, {"--note", "x", "--append", "--force-duplicate"},
		} {
			e := newEnv(t).seed()
			if r := e.run(false, "", append(append([]string{}, tc.args...), form...)...); r.code != 0 {
				t.Fatalf("%v %v: %+v", tc.args, form, r)
			}
			bodies := e.srv.LoggedBodies(tc.suffix)
			if len(bodies) != 1 {
				t.Fatalf("%v %v: %d writes", tc.args, form, len(bodies))
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(bodies[0]), &m); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, m)
		}
		for i, m := range sent {
			if _, ok := m["note"]; ok || m["appendNote"] != "x" {
				t.Errorf("%v form %d sent %v", tc.args, i, m)
			}
			if dup := i >= 2; (m["allowDuplicateNote"] == true) != dup {
				t.Errorf("%v form %d sent %v", tc.args, i, m)
			}
		}
		if a, b := jsonString(sent[0]), jsonString(sent[1]); a != b {
			t.Errorf("%v: %s and %s differ", tc.args, a, b)
		}
		if a, b := jsonString(sent[2]), jsonString(sent[3]); a != b {
			t.Errorf("%v: %s and %s differ", tc.args, a, b)
		}
	}
}

func jsonString(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

func TestAppendOutput(t *testing.T) {
	e := newEnv(t).seed()
	id := e.ids["backup"] // note "Finished 02:14, 3.2 GB"
	r := e.run(true, "", "tick", "Nightly backup", "--append-note", "checked")
	if r.code != 0 || r.stdout != "✓ Nightly backup · Sun 18 Oct  📝 Finished 02:14, 3.2 GB …\n" {
		t.Errorf("pretty: %+v", r)
	}
	r = e.run(false, "", "add", "Cups of coffee", "--note", "decaf", "--append", "--json")
	var out writeResult
	if err := json.Unmarshal([]byte(r.stdout), &out); r.code != 0 || err != nil || out.Note == nil || *out.Note != "decaf" || out.Value.Int() != 4 {
		t.Errorf("json: %+v", r)
	}
	r = e.run(false, "", "note", "Nightly backup", "more", "--append", "--json")
	if err := json.Unmarshal([]byte(r.stdout), &out); r.code != 0 || err != nil || out.Note == nil || *out.Note != "Finished 02:14, 3.2 GB\nchecked\nmore" {
		t.Errorf("note --append json: %+v", r)
	}
	if r := e.run(true, "", "note", "Nightly backup", "more", "--append"); r.code != 0 || r.stdout != "Nightly backup · Sun 18 Oct  📝 Finished 02:14, 3.2 GB …\n" {
		t.Errorf("note --append pretty: %+v", r)
	}
	if n, _ := e.srv.Note(id, "2026-10-18"); n != "Finished 02:14, 3.2 GB\nchecked\nmore" {
		t.Errorf("note = %q", n)
	}
}

func TestNoteAppend(t *testing.T) {
	e := newEnv(t).seed()
	id := e.ids["ex"]
	for _, text := range []string{"a", "a", "b"} {
		if r := e.run(false, "", "note", "Exercise", text, "--append"); r.code != 0 {
			t.Fatalf("%s: %+v", text, r)
		}
	}
	if r := e.run(false, "", "note", "Exercise", "a", "--append", "--force-duplicate"); r.code != 0 {
		t.Fatalf("--force-duplicate: %+v", r)
	}
	if n, _ := e.srv.Note(id, "2026-10-18"); n != "a\nb\na" {
		t.Errorf("note = %q", n)
	}
	bodies := e.srv.LoggedBodies("/notes/append")
	if len(bodies) != 4 || strings.Contains(bodies[0], "allowDuplicate") || !strings.Contains(bodies[3], `"allowDuplicate":true`) {
		t.Errorf("bodies = %q", bodies)
	}
	if e.srv.Count("POST /ocs/v2.php/apps/tickbuddy/api/notes/set") != 0 {
		t.Error("note --append replaced the note")
	}
}

func TestAppendArchivedInBetween(t *testing.T) {
	for _, args := range [][]string{
		{"tick", "Made someone smile", "--append-note", "x"},
		{"untick", "Made someone smile", "--note", "x", "--append"},
		{"note", "Made someone smile", "x", "--append"},
	} {
		e := newEnv(t).seed()
		srv, id := e.srv, e.ids["smile"]
		srv.SetTick(id, "2026-10-18", 0)
		srv.Config.Handler = wrap(srv.Config.Handler, func(path string) {
			if strings.HasSuffix(path, "/ticks/set") || strings.HasSuffix(path, "/notes/append") {
				srv.Update(id, func(t *fakeserver.Track) { t.ArchivedAt = "2026-10-18" })
			}
		})
		if r := e.run(false, "", args...); r.code != 1 || !strings.Contains(r.stderr, "is archived") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestAppendRetries(t *testing.T) {
	e := newEnv(t).seed()
	e.srv.ApplyThenFail = 1
	if r := e.run(false, "", "tick", "Made someone smile", "--append-note", "x"); r.code != 0 {
		t.Errorf("tick through a lost answer: %+v", r)
	}
	if n, _ := e.srv.Note(e.ids["smile"], "2026-10-18"); n != "x" {
		t.Errorf("note = %q", n)
	}

	e.srv.ApplyThenFail = 1
	if r := e.run(false, "", "tick", "Exercise", "--append-note", "y", "--force-duplicate"); r.code != 5 {
		t.Errorf("duplicate append: %+v", r)
	}
	if n := len(e.srv.LoggedBodies("/ticks/set")); n != 3 {
		t.Errorf("set sent %d times, want 2 then 1", n)
	}

	e.srv.ApplyThenFail = 1
	r := e.run(false, "", "add", "Cups of coffee", "--append-note", "z")
	if r.code != 5 || !strings.Contains(r.stderr, "may or may not have counted this") {
		t.Errorf("add: %+v", r)
	}
	if n := e.srv.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment"); n != 1 {
		t.Errorf("add sent %d times", n)
	}

	// A 409 after the tick: the tick isn't repeated, the note is appended
	e.srv.ConflictNext = 1
	if r := e.run(false, "", "add", "Cups of coffee", "--append-note", "w"); r.code != 0 {
		t.Errorf("add after a 409: %+v", r)
	}
	if n := e.srv.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment"); n != 2 || e.srv.Tick(e.ids["coffee"], "2026-10-18") != 5 {
		t.Errorf("increment sent %d times in all, count %d", n, e.srv.Tick(e.ids["coffee"], "2026-10-18"))
	}
}
