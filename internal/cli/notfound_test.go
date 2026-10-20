// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

// TestNotFoundKinds: Tickbuddy's 404 for a track gone since it was listed,
// with a reason or only a message, exits 4; Nextcloud's own 404 with empty
// data, as while the app is disabled, exits 1 saying so; a name that matches
// nothing locally exits 4 as before.
func TestNotFoundKinds(t *testing.T) {
	const notInstalled = "Tickbuddy isn't installed or enabled on this Nextcloud"
	cases := []struct {
		kind string
		prep func(e *env)
		code int
		msg  string
	}{
		{"reason", func(e *env) { e.srv.NotFoundReason = "trackNotFound" }, 4, "Track not found"},
		{"message", func(e *env) {}, 4, "Track not found"},
		{"disabled", func(e *env) { e.srv.Disabled = true }, 1, notInstalled},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			for _, args := range [][]string{
				{"tick", "Exercise"}, {"set", "Cups of coffee", "2"}, {"note", "Exercise", "x"},
			} {
				e := newEnv(t).seed()
				tc.prep(e)
				srv := e.srv
				// The track goes after the list, before the write.
				srv.Config.Handler = wrap(srv.Config.Handler, func(path string) {
					if strings.Contains(path, "/api/ticks/") || strings.Contains(path, "/api/notes/") {
						for _, id := range []int{e.ids["ex"], e.ids["coffee"]} {
							srv.Update(id, func(t *tickbuddytest.Track) { t.ID += 100 })
						}
					}
				})
				r := e.run(false, "", args...)
				if r.code != tc.code || r.stderr != "tickcli: "+tc.msg+"\n" || r.stdout != "" {
					t.Errorf("%v: %+v", args, r)
				}
			}
		})
	}

	e := newEnv(t).seed()
	e.srv.Disabled = true
	r := e.run(false, "", "tracks", "--json")
	var got struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}
	if err := json.Unmarshal([]byte(r.stderr), &got); err != nil || r.code != 1 || got.Code != 1 || got.Error != notInstalled {
		t.Errorf("tracks --json, disabled: %+v", r)
	}

	e = newEnv(t).seed()
	if r := e.run(false, "", "tick", "Nonexistent"); r.code != 4 || !strings.Contains(r.stderr, `no track called "Nonexistent"`) {
		t.Errorf("no local match: %+v", r)
	}
}
