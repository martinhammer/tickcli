// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/internal/fakeserver"
	"github.com/martinhammer/tickcli/tickbuddy"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// now is Sat 18 Oct 2026, noon local time.
var now = time.Date(2026, time.October, 18, 12, 0, 0, 0, time.Local)

type env struct {
	t   *testing.T
	srv *fakeserver.Server
	env map[string]string
	dir string
	ids map[string]int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := fakeserver.New()
	t.Cleanup(s.Close)
	e := &env{t: t, srv: s, dir: t.TempDir(), ids: map[string]int{}}
	e.env = map[string]string{
		tickbuddy.EnvURL: s.URL, tickbuddy.EnvUser: s.User, tickbuddy.EnvAppPassword: s.Password,
		"TERM": "xterm", "NO_COLOR": "1",
	}
	return e
}

// seed adds the tracks of the spec's examples.
func (e *env) seed() *env {
	s := e.srv
	e.ids["ex"] = s.AddTrack("Exercise", "boolean")
	e.ids["smile"] = s.AddTrack("Made someone smile", "boolean")
	e.ids["coffee"] = s.AddTrack("Cups of coffee", "counter")
	e.ids["backup"] = s.AddTrack("Nightly backup", "boolean")
	e.ids["diary"] = s.AddTrack("Diary", "boolean")
	e.ids["old"] = s.AddTrack("Old habit", "boolean")
	s.Update(e.ids["diary"], func(t *fakeserver.Track) { t.Private = true })
	s.Update(e.ids["old"], func(t *fakeserver.Track) { t.ArchivedAt = "2026-09-01" })
	s.SetTick(e.ids["ex"], "2026-10-18", 1)
	s.SetTick(e.ids["coffee"], "2026-10-18", 3)
	s.SetTick(e.ids["backup"], "2026-10-18", 1)
	s.SetTick(e.ids["diary"], "2026-10-18", 1)
	s.SetNote(e.ids["backup"], "2026-10-18", "Finished 02:14, 3.2 GB")
	for i, d := range []string{"2026-10-17", "2026-10-16", "2026-10-14", "2026-10-13", "2026-10-12"} {
		s.SetTick(e.ids["ex"], d, 1)
		s.SetTick(e.ids["coffee"], d, []int{2, 4, 12, 2, 3}[i])
	}
	s.Stats[e.ids["ex"]] = map[string]any{
		"total": 212, "weeklyMean": 4.3, "last7": 5, "previous7": 4, "peak7": 7,
		"current":       map[string]any{"isStreak": true, "length": 3, "from": "2026-10-16", "to": "2026-10-18"},
		"longestStreak": map[string]any{"isStreak": true, "length": 21, "from": "2026-03-02", "to": "2026-03-22"},
		"longestBreak":  map[string]any{"isStreak": false, "length": 9, "from": "2026-08-04", "to": "2026-08-12"},
		"firstTick":     "2025-01-03", "lastTick": "2026-10-18", "maxValue": 1, "minValue": 1,
	}
	s.Stats[e.ids["coffee"]] = map[string]any{
		"total": 950, "weeklyMean": 17.25, "last7": 26,
		"current":       map[string]any{"isStreak": true, "length": 3, "from": "2026-10-15", "to": "2026-10-17"},
		"upToYesterday": true,
	}
	s.Stats[e.ids["smile"]] = map[string]any{
		"current": map[string]any{"isStreak": false, "length": 1, "from": "2026-10-18", "to": "2026-10-18"},
	}
	return e
}

type result struct {
	code   int
	stdout string
	stderr string
}

// run runs tickcli; tty makes stdout and stdin terminals.
func (e *env) run(tty bool, stdin string, args ...string) result {
	e.t.Helper()
	var out, errb bytes.Buffer
	a := &App{
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
		StdinTTY: tty, StdoutTTY: tty,
		Getenv: func(k string) string { return e.env[k] },
		Now:    func() time.Time { return now },
		Store: &tickbuddy.Store{
			Getenv: func(k string) string { return e.env[k] },
			Dir:    filepath.Join(e.dir, "tickcli"), MachineIDPath: filepath.Join(e.dir, "machine-id"),
			UID: 1000, GOOS: "linux",
		},
		Hostname:     "testhost",
		ReadPassword: func() ([]byte, error) { return []byte("app-pass"), nil },
		PollInterval: time.Millisecond,
	}
	code := a.Run(context.Background(), args)
	return result{code, out.String(), errb.String()}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/cli -update)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

// TestGolden runs each command in pretty (colour off), plain and JSON form.
func TestGolden(t *testing.T) {
	cmds := map[string][]string{
		"today":           {},
		"today-yesterday": {"-d", "yesterday", "--private"},
		"week":            {"week"},
		"stats-one":       {"stats", "Exercise"},
		"stats-all":       {"stats"},
		"stats-year":      {"stats", "Exercise", "--year", "2025"},
		"stats-all-year":  {"stats", "--year", "2025"},
		"tracks":          {"tracks", "--archived", "--private"},
		"notes":           {"notes"},
		"status":          {"status"},
	}
	for name, args := range cmds {
		for _, m := range []string{"pretty", "plain", "json"} {
			t.Run(name+"-"+m, func(t *testing.T) {
				e := newEnv(t).seed()
				e.srv.SetNote(e.ids["ex"], "2026-10-16", "Ran 5 km\nin the rain")
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
				golden(t, name+"-"+m, strings.ReplaceAll(r.stdout, e.srv.URL, "https://cloud.example.com"))
			})
		}
	}
}

func TestWrites(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"tick", []string{"tick", "made someone smile"}},
		{"untick", []string{"untick", "Exercise"}},
		{"set", []string{"set", "Cups of coffee", "4"}},
		{"add", []string{"add", "Cups of coffee"}},
		{"add-negative", []string{"add", "Cups of coffee", "-2", "-d", "-1"}},
		{"tick-note", []string{"tick", "Nightly backup", "--note", "Finished 02:14, 3.2 GB"}},
		{"tick-note-clear", []string{"tick", "Nightly backup", "--note", ""}},
		{"note", []string{"note", "Exercise", "Ran 5 km"}},
		{"note-clear", []string{"note", "Nightly backup", "--clear"}},
	}
	for _, tc := range cases {
		for _, m := range []string{"pretty", "plain", "json"} {
			t.Run(tc.name+"-"+m, func(t *testing.T) {
				e := newEnv(t).seed()
				a := tc.args
				if m == "json" {
					a = append(append([]string{}, a...), "--json")
				}
				r := e.run(m == "pretty", "", a...)
				if r.code != 0 {
					t.Fatalf("exit %d: %s", r.code, r.stderr)
				}
				if m == "plain" && r.stdout != "" {
					t.Errorf("plain write printed %q", r.stdout)
				}
				if m != "plain" {
					golden(t, "write-"+tc.name+"-"+m, r.stdout)
				}
			})
		}
	}

	e := newEnv(t).seed()
	e.run(false, "", "add", "Cups of coffee", "-2", "-d", "-1")
	if got := e.srv.Tick(e.ids["coffee"], "2026-10-17"); got != 0 {
		t.Errorf("add -2 on yesterday's 2 left %d", got)
	}
	e.run(false, "", "set", "#3", "0")
	if got := e.srv.Tick(e.ids["coffee"], "2026-10-18"); got != 0 {
		t.Errorf("set 0 left %d", got)
	}
}

func TestBackupRecipe(t *testing.T) {
	e := newEnv(t).seed()
	id := e.ids["backup"]
	e.srv.SetTick(id, "2026-10-18", 0)
	e.srv.SetNote(id, "2026-10-18", "")
	before := e.srv.Count("POST")
	if r := e.run(false, "", "tick", "Nightly backup", "--note", "02:14"); r.code != 0 || r.stdout != "" {
		t.Fatalf("first run: %+v", r)
	}
	if n := e.srv.Count("POST") - before; n != 1 {
		t.Errorf("ticked and stamped in %d requests", n)
	}
	if r := e.run(false, "", "tick", "Nightly backup", "--note", "02:20"); r.code != 0 {
		t.Fatalf("second run: %+v", r)
	}
	if e.srv.Tick(id, "2026-10-18") != 1 {
		t.Error("not ticked")
	}
	if n, _ := e.srv.Note(id, "2026-10-18"); n != "02:20" {
		t.Errorf("note = %q", n)
	}
}

func TestExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		prep   func(e *env)
		args   []string
		code   int
		stderr string
	}{
		{"unknown command", nil, []string{"frobnicate"}, 2, "unknown command"},
		{"unknown flag", nil, []string{"tick", "x", "--bogus"}, 2, "unknown flag"},
		{"arguments", nil, []string{"tick"}, 2, "wrong number of arguments"},
		{"bad date", nil, []string{"tick", "Exercise", "-d", "2026-02-30"}, 2, "not a date"},
		{"bad count", nil, []string{"set", "Cups of coffee", "x"}, 2, "isn't a count"},
		{"add zero", nil, []string{"add", "Cups of coffee", "0"}, 2, "other than 0"},
		{"set number first", nil, []string{"set", "5", "Cups of coffee"}, 2, "the track comes first: tickcli set <track> <n>"},
		{"add number first", nil, []string{"add", "2", "Cups of coffee"}, 2, "the track comes first"},
		{"add negative first", nil, []string{"add", "-1", "Cups of coffee"}, 2, "the track comes first"},
		{"type mismatch", nil, []string{"tick", "Cups of coffee"}, 2, "Cups of coffee is a counter; use `tickcli add` or `tickcli set`"},
		{"type mismatch add", nil, []string{"add", "Exercise"}, 2, "yes/no track"},
		{"not signed in", func(e *env) { e.env = map[string]string{} }, []string{"tracks"}, 3, "not signed in"},
		{"two of three", func(e *env) { delete(e.env, tickbuddy.EnvUser) }, []string{"tracks"}, 3, "TICKBUDDY_USER isn't"},
		{"refused", func(e *env) { e.env[tickbuddy.EnvAppPassword] = "nope" }, []string{"tracks"}, 3, "refused the user and app password in the TICKBUDDY_* environment variables"},
		{"not found", nil, []string{"tick", "Walk"}, 4, `no track called "Walk"`},
		{"prefix in a script", nil, []string{"tick", "Exer"}, 4, "did you mean Exercise (#1)"},
		{"unreachable", func(e *env) { e.srv.Close() }, []string{"tracks", "--retries", "0"}, 5, "can't reach"},
		{"archived", nil, []string{"tick", "Old habit"}, 1, `Track "Old habit" is archived, so nothing was changed`},
		{"archived note", nil, []string{"note", "Old habit", "x"}, 1, "archived"},
		{"no tickbuddy", func(e *env) { e.srv.NoTickbuddy = true }, []string{"tracks"}, 1, "Tickbuddy isn't installed or enabled on this Nextcloud"},
		{"old server stats", func(e *env) { e.srv.Old() }, []string{"stats"}, 1, "newer Tickbuddy"},
		{"old server stats period", func(e *env) { e.srv.Old() }, []string{"stats", "--year", "2025"}, 1, "newer Tickbuddy"},
		{"no statsPeriod", func(e *env) { e.srv.Features["statsPeriod"] = false }, []string{"stats", "Exercise", "--from", "-30"}, 1, `newer Tickbuddy on the server (it lacks "statsPeriod")`},
		{"old server week", func(e *env) { e.srv.Old() }, []string{"week"}, 1, "newer Tickbuddy"},
		{"old server notes", func(e *env) { e.srv.Old() }, []string{"notes"}, 1, "newer Tickbuddy"},
		{"old server --note", func(e *env) { e.srv.Old() }, []string{"tick", "Exercise", "--note", "x"}, 1, "newer Tickbuddy"},
		{"note too long", nil, []string{"note", "Exercise", strings.Repeat("x", 501)}, 1, "Notes can be at most 500 characters"},
		{"note too long before notesAppend", func(e *env) { e.srv.Features["notesAppend"] = false }, []string{"note", "Exercise", strings.Repeat("x", 201)}, 1, "Notes can be at most 200 characters"},
		{"append too long", func(e *env) { e.srv.SetNote(e.ids["ex"], "2026-10-18", strings.Repeat("x", 450)) }, []string{"tick", "Exercise", "--append-note", strings.Repeat("y", 50)}, 1, "The note would be longer than 500 characters"},
		{"note --append too long", func(e *env) { e.srv.SetNote(e.ids["ex"], "2026-10-18", strings.Repeat("x", 450)) }, []string{"note", "Exercise", strings.Repeat("y", 50), "--append"}, 1, "The note would be longer than 500 characters"},
		{"note and clear", nil, []string{"note", "Exercise", "x", "--clear"}, 2, "--clear"},
		{"note and append-note", nil, []string{"tick", "Exercise", "--note", "x", "--append-note", "y"}, 2, "use --note or --append-note, not both"},
		{"append without note", nil, []string{"tick", "Exercise", "--append"}, 2, "--append goes with --note"},
		{"append with append-note", nil, []string{"add", "Cups of coffee", "--append-note", "y", "--append"}, 2, "--append goes with --note"},
		{"note append and clear", nil, []string{"note", "Exercise", "--append", "--clear"}, 2, "use --append or --clear, not both"},
		{"force-duplicate alone", nil, []string{"set", "Cups of coffee", "2", "--force-duplicate"}, 2, "--force-duplicate only goes with an append"},
		{"force-duplicate with note", nil, []string{"untick", "Exercise", "--note", "x", "--force-duplicate"}, 2, "--force-duplicate only goes with an append"},
		{"note force-duplicate without append", nil, []string{"note", "Exercise", "x", "--force-duplicate"}, 2, "--force-duplicate only goes with an append"},
		{"no notesAppend", func(e *env) { e.srv.Features["notesAppend"] = false }, []string{"tick", "Exercise", "--append-note", "x"}, 1, `it lacks "notesAppend"`},
		{"no notesAppend note --append", func(e *env) { e.srv.Features["notesAppend"] = false }, []string{"note", "Exercise", "x", "--append"}, 1, `it lacks "notesAppend"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t).seed()
			if tc.prep != nil {
				tc.prep(e)
			}
			before := e.srv.Count("POST")
			r := e.run(false, "", tc.args...)
			if r.code != tc.code || !strings.Contains(r.stderr, tc.stderr) {
				t.Errorf("exit %d, stderr %q; want %d with %q", r.code, r.stderr, tc.code, tc.stderr)
			}
			if !strings.HasPrefix(r.stderr, "tickcli: ") {
				t.Errorf("stderr %q", r.stderr)
			}
			if r.stdout != "" {
				t.Errorf("stdout %q", r.stdout)
			}
			if tc.code != 0 && !strings.Contains(tc.name, "too long") && e.srv.Count("POST") != before {
				t.Errorf("wrote despite failing")
			}
		})
	}
}

func TestJSONError(t *testing.T) {
	e := newEnv(t).seed()
	r := e.run(false, "", "tick", "Walk", "--json")
	var got struct {
		Error string `json:"error"`
		Code  int    `json:"code"`
	}
	if err := json.Unmarshal([]byte(r.stderr), &got); err != nil || got.Code != 4 || r.code != 4 || !strings.Contains(got.Error, "Walk") {
		t.Errorf("stderr %q, exit %d", r.stderr, r.code)
	}
}

func TestPrefixWhenInteractive(t *testing.T) {
	e := newEnv(t).seed()
	if r := e.run(true, "", "tick", "made"); r.code != 0 {
		t.Errorf("prefix at a terminal: %+v", r)
	}
	if r := e.run(true, "", "tick", "cups"); r.code != 2 { // counter, not ambiguous
		t.Errorf("cups: %+v", r)
	}
}

func TestArchivedInBetween(t *testing.T) {
	e := newEnv(t).seed()
	// The track list says active, but the server has it archived by the
	// time the write arrives.
	srv := e.srv
	id := e.ids["smile"]
	orig := srv.Config.Handler
	srv.Config.Handler = wrap(orig, func(path string) {
		if strings.HasSuffix(path, "/ticks/set") || strings.HasSuffix(path, "/notes/set") {
			srv.Update(id, func(t *fakeserver.Track) { t.ArchivedAt = "2026-10-18" })
		}
	})
	r := e.run(false, "", "tick", "Made someone smile")
	if r.code != 1 || !strings.Contains(r.stderr, "is archived") {
		t.Errorf("tick: %+v", r)
	}
	srv.Update(id, func(t *fakeserver.Track) { t.ArchivedAt = "" })
	srv.SetNote(id, "2026-10-18", "kept")
	r = e.run(false, "", "note", "Made someone smile", "--clear")
	if r.code != 1 || !strings.Contains(r.stderr, "is archived") {
		t.Errorf("note --clear: %+v", r)
	}
}

// wrap calls hook with each request's path before h serves it.
func wrap(h http.Handler, hook func(path string)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hook(r.URL.Path)
		h.ServeHTTP(w, r)
	})
}

func TestOldServerFallbacks(t *testing.T) {
	e := newEnv(t).seed()
	e.srv.Old()
	for _, args := range [][]string{
		{"tick", "Made someone smile"}, {"untick", "Exercise"}, {"set", "Cups of coffee", "5"}, {"add", "Cups of coffee", "2"},
	} {
		if r := e.run(false, "", args...); r.code != 0 {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if e.srv.Tick(e.ids["smile"], "2026-10-18") != 1 || e.srv.Tick(e.ids["ex"], "2026-10-18") != 0 || e.srv.Tick(e.ids["coffee"], "2026-10-18") != 7 {
		t.Error("fallback writes didn't land")
	}
	if r := e.run(false, ""); r.code != 0 || strings.Contains(r.stdout, "Finished") {
		t.Errorf("today on an old server: %+v", r)
	}
}

func TestRetries(t *testing.T) {
	e := newEnv(t).seed()
	r := e.run(false, "", "tracks") // warm-up isn't shared; each run is fresh
	if r.code != 0 {
		t.Fatal(r)
	}
	e.srv.FailNext = 1
	if r := e.run(false, "", "tick", "Made someone smile"); r.code != 0 {
		t.Errorf("tick through an outage: %+v", r)
	}

	e.srv.ApplyThenFail = 1
	before := e.srv.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment")
	r = e.run(false, "", "add", "Cups of coffee")
	if r.code != 5 || !strings.Contains(r.stderr, "may or may not have counted this; check with `tickcli` before adding again") {
		t.Errorf("add: %+v", r)
	}
	if n := e.srv.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment") - before; n != 1 {
		t.Errorf("add sent %d times", n)
	}
}

func TestCleaning(t *testing.T) {
	e := newEnv(t)
	e.srv.AddTrack("\x1b[2JEvil\u009b", "boolean")
	for _, tty := range []bool{true, false} {
		r := e.run(tty, "")
		if strings.ContainsAny(r.stdout, "\x1b\u009b") || !strings.Contains(r.stdout, "[2JEvil") {
			t.Errorf("tty=%v: %q", tty, r.stdout)
		}
	}
	if got := clean("a\x00b\x7fc\td\ne\xffg"); got != "abc\td\neg" {
		t.Errorf("clean = %q", got)
	}
	if got := flat("a\tb\nc"); got != "a b c" {
		t.Errorf("flat = %q", got)
	}
}

func TestColour(t *testing.T) {
	e := newEnv(t).seed()
	delete(e.env, "NO_COLOR")
	if r := e.run(true, ""); !strings.Contains(r.stdout, "\x1b[32m✓") {
		t.Errorf("no colour: %q", r.stdout)
	}
	e.env["TERM"] = "dumb"
	if r := e.run(true, ""); strings.Contains(r.stdout, "\x1b[") || !strings.Contains(r.stdout, "✓") {
		t.Errorf("TERM=dumb: %q", r.stdout)
	}
}

func TestExport(t *testing.T) {
	e := newEnv(t).seed()
	r := e.run(false, "", "export")
	if r.code != 0 || !json.Valid([]byte(r.stdout)) || strings.Contains(r.stdout, "Diary") {
		t.Errorf("export: %+v", r)
	}
	if r := e.run(false, "", "export", "--private"); !strings.Contains(r.stdout, "Diary") {
		t.Errorf("export --private: %+v", r)
	}
}

func TestLoginPasteAndLogout(t *testing.T) {
	e := newEnv(t).seed()
	url := e.srv.URL
	e.env = map[string]string{"NO_COLOR": "1"}
	_ = os.WriteFile(filepath.Join(e.dir, "machine-id"), []byte("0123456789abcdef0123456789abcdef\n"), 0o444)

	r := e.run(false, "martin\napp-pass\n", "login", url, "--paste")
	if r.code != 0 || r.stdout != "Signed in as martin · Tickbuddy 2.3.0\n" || !strings.Contains(r.stderr, "machine id") {
		t.Fatalf("login: %+v", r)
	}
	if r := e.run(false, "", "status"); r.code != 0 || !strings.Contains(r.stdout, "credentials\tfile (machine id)") {
		t.Errorf("status: %+v", r)
	}
	if r := e.run(false, "", "tick", "Made someone smile"); r.code != 0 {
		t.Errorf("tick after login: %+v", r)
	}
	r = e.run(false, "", "logout")
	if r.code != 0 || e.srv.Revoked || !strings.Contains(r.stderr, "still works") {
		t.Errorf("logout of a pasted password: %+v revoked=%v", r, e.srv.Revoked)
	}
	if r := e.run(false, "", "status"); r.code != 3 {
		t.Errorf("status after logout: %+v", r)
	}

	// wrong password: nothing stored, exit 3
	r = e.run(false, "martin\nwrong\n", "login", url, "--paste")
	if r.code != 3 {
		t.Errorf("wrong password: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.dir, "tickcli", "credentials")); !os.IsNotExist(err) {
		t.Error("stored after a refusal")
	}
	// plain http to a public host
	if r := e.run(false, "", "login", "http://cloud.example.com"); r.code != 2 || !strings.Contains(r.stderr, "--insecure-http") {
		t.Errorf("http: %+v", r)
	}
}

func TestLoginLinkAndLogout(t *testing.T) {
	e := newEnv(t).seed()
	url := e.srv.URL
	e.env = map[string]string{"NO_COLOR": "1"}
	_ = os.WriteFile(filepath.Join(e.dir, "machine-id"), []byte("0123456789abcdef0123456789abcdef\n"), 0o444)
	e.srv.LoginPolls = 2

	r := e.run(false, "", "login", url, "--qr")
	if r.code != 0 || !strings.Contains(r.stderr, url+"/index.php/login/v2/flow/abc") || !strings.Contains(r.stderr, "█") {
		t.Fatalf("login: %+v", r)
	}
	if r := e.run(false, "", "today"); r.code != 2 {
		t.Errorf("`today` isn't a command: %+v", r)
	}
	r = e.run(false, "", "logout")
	if r.code != 0 || !e.srv.Revoked || !strings.Contains(r.stderr, "revoked") {
		t.Errorf("logout: %+v revoked=%v", r, e.srv.Revoked)
	}
	if r := e.run(false, "", "logout"); r.code != 0 || !strings.Contains(r.stderr, "nothing to forget") {
		t.Errorf("second logout: %+v", r)
	}
}

func TestLogoutWithEnvironment(t *testing.T) {
	e := newEnv(t)
	if r := e.run(true, "", "logout"); r.code != 0 || !strings.Contains(r.stdout, "nothing stored to forget") {
		t.Errorf("%+v", r)
	}
}

func TestProtectNegatives(t *testing.T) {
	cases := map[string]string{
		"add c -2":                 "add -- c -2",
		"add c -2 -d -1":           "-d -1 add -- c -2",
		"add -2 c":                 "add -- -2 c",
		"--json add c -2 --note x": "--json --note x add -- c -2",
		"tick x -d -1":             "tick x -d -1",
		"add c --note -3":          "add c --note -3",
		"add c -- -2":              "add c -- -2",
	}
	for in, want := range cases {
		if got := strings.Join(protectNegatives(strings.Fields(in)), " "); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestVersion(t *testing.T) {
	e := newEnv(t)
	if r := e.run(false, "", "--version"); r.code != 0 || r.stdout != "tickcli dev\n" {
		t.Errorf("%+v", r)
	}
}

func TestStatsPeriod(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		from, to string // "" for not sent
	}{
		{"all time", nil, "", ""},
		{"days ago", []string{"--from", "-30"}, "2026-09-18", ""},
		{"yesterday", []string{"--to", "yesterday"}, "", "2026-10-17"},
		{"full dates", []string{"--from", "2025-03-01", "--to", "2025-06-30"}, "2025-03-01", "2025-06-30"},
		{"year", []string{"--year", "2025"}, "2025-01-01", "2025-12-31"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t).seed()
			r := e.run(false, "", append([]string{"stats", "Exercise"}, tc.args...)...)
			if r.code != 0 {
				t.Fatalf("exit %d: %s", r.code, r.stderr)
			}
			q := e.srv.StatsQueries[len(e.srv.StatsQueries)-1]
			for k, want := range map[string]string{"from": tc.from, "to": tc.to} {
				if _, sent := q[k]; q.Get(k) != want || sent != (want != "") {
					t.Errorf("%s = %q (sent %v), want %q", k, q.Get(k), sent, want)
				}
			}
		})
	}

	bad := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"bad date", []string{"--from", "2025-02-30"}, "not a date"},
		{"from after to", []string{"--from", "today", "--to", "yesterday"}, "--from must be on or before --to"},
		{"bad year", []string{"--year", "25"}, "--year"},
		{"year and from", []string{"--year", "2025", "--from", "-1"}, "Use --year or --from/--to, not both"},
		{"year and to", []string{"--year", "2025", "--to", "today"}, "Use --year or --from/--to, not both"},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t).seed()
			r := e.run(false, "", append([]string{"stats", "Exercise"}, tc.args...)...)
			if r.code != 2 || !strings.Contains(r.stderr, tc.stderr) {
				t.Errorf("exit %d, stderr %q; want 2 with %q", r.code, r.stderr, tc.stderr)
			}
			if n := len(e.srv.Logged()); n != 0 {
				t.Errorf("sent %d requests", n)
			}
		})
	}

	// The last run line: "At the end" for a period that ended, else
	// "Current streak".
	for _, tc := range []struct {
		args    []string
		heading string
		line    string
	}{
		{[]string{"--year", "2025"}, "Exercise, 1 Jan – 31 Dec 2025", "  At the end        3-day streak\n"},
		{[]string{"--year", "2026"}, "Exercise, 1 Jan – 31 Dec 2026", "  Current streak    3 days   (since 16 Oct)\n"},
		{[]string{"--to", "today"}, "Exercise, up to 18 Oct 2026", "  Current streak"},
		{[]string{"--to", "yesterday"}, "Exercise, up to 17 Oct 2026", "  At the end"},
		{[]string{"--from", "2025-12-01", "--to", "2026-01-31"}, "Exercise, 1 Dec 2025 – 31 Jan 2026", "  At the end"},
		{[]string{"--from", "-30"}, "Exercise, since 18 Sep 2026", "  Current streak"},
	} {
		e := newEnv(t).seed()
		r := e.run(true, "", append([]string{"stats", "Exercise"}, tc.args...)...)
		if !strings.HasPrefix(r.stdout, tc.heading+"\n") || !strings.Contains(r.stdout, tc.line) {
			t.Errorf("%v:\n%s", tc.args, r.stdout)
		}
	}

	e := newEnv(t).seed()
	e.srv.Stats[e.ids["ex"]]["upToYesterday"] = true
	r := e.run(true, "", "stats", "Exercise", "--year", "2025")
	if !strings.Contains(r.stdout, "  At the end        3-day streak (to 18 Oct)\n") {
		t.Errorf("up to the day before the end:\n%s", r.stdout)
	}
}
