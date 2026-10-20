// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

// readOnlyWrites are the writes to Nightly backup, each with a value or note
// the server's 200 would show unchanged.
var readOnlyWrites = []struct {
	args    []string
	counter bool
}{
	{[]string{"tick", "Nightly backup", "--note", "02:20"}, false},
	{[]string{"untick", "Nightly backup"}, false},
	{[]string{"set", "Nightly backup", "2"}, true},
	{[]string{"add", "Nightly backup", "--note", "02:20"}, true},
	{[]string{"note", "Nightly backup", "02:20"}, false},
	{[]string{"note", "Nightly backup", "02:20", "--append"}, false},
	{[]string{"note", "Nightly backup", "--clear"}, false},
}

// checkReadOnly runs args plain and with --json and wants exit 6 with msg,
// and nothing changed on the server.
func checkReadOnly(t *testing.T, e *env, args []string, msg string) {
	t.Helper()
	id := e.ids["backup"]
	r := e.run(false, "", args...)
	if r.code != 6 || r.stderr != "tickcli: "+msg+"\n" || r.stdout != "" {
		t.Errorf("%v: %+v", args, r)
	}
	r = e.run(false, "", append(args, "--json")...)
	var got struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}
	if err := json.Unmarshal([]byte(r.stderr), &got); err != nil || r.code != 6 || got.Code != 6 || got.Error != msg || r.stdout != "" {
		t.Errorf("%v --json: %+v", args, r)
	}
	if v := e.srv.Tick(id, "2026-10-18"); v != 1 {
		t.Errorf("%v: value changed to %d", args, v)
	}
	if n, _ := e.srv.Note(id, "2026-10-18"); n != "Finished 02:14, 3.2 GB" {
		t.Errorf("%v: note changed to %q", args, n)
	}
}

// TestReadOnly: the three ways Tickcli finds an archived track end the same
// (decisions 25 and 26): the refusal before sending, a 200 that differs from
// what was sent, and the server's 423.
func TestReadOnly(t *testing.T) {
	const msg = `Track "Nightly backup" is archived, so nothing was changed`
	ways := map[string]func(e *env, id int){
		"before sending": func(e *env, id int) {
			e.srv.Update(id, func(t *tickbuddytest.Track) { t.ArchivedAt = "2026-10-01" })
		},
		"archived in between": func(e *env, id int) {
			srv := e.srv
			srv.Config.Handler = wrap(srv.Config.Handler, func(path string) {
				for _, w := range []string{"/ticks/set", "/ticks/toggle", "/ticks/increment", "/notes/set", "/notes/append"} {
					if strings.HasSuffix(path, w) {
						srv.Update(id, func(t *tickbuddytest.Track) { t.ArchivedAt = "2026-10-18" })
					}
				}
			})
		},
		"423": func(e *env, id int) {
			e.srv.Update(id, func(t *tickbuddytest.Track) { t.ReadOnly = "archived" })
		},
	}
	for name, prep := range ways {
		t.Run(name, func(t *testing.T) {
			for _, w := range readOnlyWrites {
				e := newEnv(t).seed()
				id := e.ids["backup"]
				if w.counter {
					e.srv.Update(id, func(t *tickbuddytest.Track) { t.Type = "counter" })
				}
				prep(e, id)
				before := e.srv.Count("POST")
				checkReadOnly(t, e, w.args, msg)
				if name == "before sending" && e.srv.Count("POST") != before {
					t.Errorf("%v: sent a write", w.args)
				}
			}
		})
	}
}

// TestReadOnlyOtherReason: a 423 for another reason shows the server's
// message, still exit 6.
func TestReadOnlyOtherReason(t *testing.T) {
	for _, w := range readOnlyWrites {
		e := newEnv(t).seed()
		id := e.ids["backup"]
		e.srv.Update(id, func(t *tickbuddytest.Track) {
			t.ReadOnly = "immutable"
			if w.counter {
				t.Type = "counter"
			}
		})
		checkReadOnly(t, e, w.args, "Nightly backup is immutable, so nothing was changed")
	}
}
