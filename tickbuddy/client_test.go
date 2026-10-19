// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/internal/fakeserver"
)

var day = Date{2026, time.October, 18}

func setup(t *testing.T) (*fakeserver.Server, *Client, *[]time.Duration) {
	t.Helper()
	s := fakeserver.New()
	t.Cleanup(s.Close)
	c := New(Account{URL: s.URL, User: s.User, AppPassword: s.Password}, WithUserAgent("Test/1"))
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return s, c, &waits
}

func TestEnvelopeAndHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"ocs":{"meta":{"status":"ok"},"data":[{"id":3,"name":"Exercise","type":"boolean","sortOrder":0,"private":false,"archivedAt":null}]}}`))
	}))
	defer srv.Close()
	c := New(Account{URL: srv.URL + "/", User: "u", AppPassword: "p"}, WithUserAgent("Tickcli/1.2.3"))
	tracks, err := c.Tracks(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || tracks[0].Name != "Exercise" || tracks[0].IsArchived() {
		t.Fatalf("tracks = %+v", tracks)
	}
	if got.Get("OCS-APIRequest") != "true" || got.Get("Accept") != "application/json" || got.Get("User-Agent") != "Tickcli/1.2.3" {
		t.Errorf("headers = %v", got)
	}
	if !strings.HasPrefix(got.Get("Authorization"), "Basic ") {
		t.Errorf("no basic auth: %v", got)
	}
}

func TestCapabilities(t *testing.T) {
	s, c, _ := setup(t)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Version != "2.3.0" || !c.Has(FeatureNotes) || c.Has("syncDelta") {
		t.Errorf("caps = %+v", caps)
	}
	// cached: one request only
	_, _ = c.Capabilities(context.Background())
	if n := s.Count("GET /ocs/v2.php/cloud/capabilities"); n != 1 {
		t.Errorf("capabilities read %d times", n)
	}

	s.NoTickbuddy = true
	c2 := New(Account{URL: s.URL, User: s.User, AppPassword: s.Password})
	if _, err := c2.Capabilities(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("no tickbuddy: %v", err)
	}
	s.NoTickbuddy = false
	s.APIVersion = 2
	if _, err := c2.Capabilities(context.Background()); !errors.Is(err, ErrUnsupportedServer) {
		t.Errorf("apiVersion 2: %v", err)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
		msg    string
	}{
		{401, `{"ocs":{"meta":{},"data":[]}}`, ErrUnauthorized, ""},
		{404, `{"ocs":{"meta":{},"data":{"message":"Track not found"}}}`, ErrTrackNotFound, "Track not found"},
		{400, `{"ocs":{"meta":{},"data":{"message":"Notes can be at most 200 characters"}}}`, ErrBadRequest, "Notes can be at most 200 characters"},
		{403, `{"ocs":{"meta":{"message":"limit"},"data":[]}}`, ErrBadRequest, "limit"},
		{500, `oops`, ErrUnreachable, ""},
		{503, ``, ErrUnreachable, ""},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := New(Account{URL: srv.URL, User: "u", AppPassword: "p"}, WithRetries(0))
		_, err := c.Ticks(context.Background(), day, day)
		srv.Close()
		if !errors.Is(err, tc.want) {
			t.Errorf("%d: err = %v, want %v", tc.status, err, tc.want)
			continue
		}
		if tc.msg != "" && err.Error() != tc.msg {
			t.Errorf("%d: message = %q, want %q", tc.status, err.Error(), tc.msg)
		}
	}

	// network failure
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c := New(Account{URL: srv.URL, User: "u", AppPassword: "p"}, WithRetries(0))
	if _, err := c.Ticks(context.Background(), day, day); !errors.Is(err, ErrUnreachable) {
		t.Errorf("closed server: %v", err)
	}
}

func TestEveryCall(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Cups of coffee", "counter")

	r, err := c.SetYesNo(ctx, ex, day, true, nil)
	if err != nil || !r.Value.IsBool() || !r.Value.Bool() || r.Archived || r.Note != nil {
		t.Fatalf("SetYesNo = %+v, %v", r, err)
	}
	r, err = c.SetCount(ctx, cof, day, 3, nil)
	if err != nil || r.Value.Int() != 3 || r.Value.IsBool() || r.Archived {
		t.Fatalf("SetCount = %+v, %v", r, err)
	}
	r, err = c.Add(ctx, cof, day, 2, nil)
	if err != nil || r.Value.Int() != 5 || r.Archived {
		t.Fatalf("Add = %+v, %v", r, err)
	}
	r, err = c.Add(ctx, cof, day, -9, nil)
	if err != nil || r.Value.Int() != 0 {
		t.Fatalf("Add below 0 = %+v, %v", r, err)
	}
	if _, err := c.Add(ctx, cof, day, 0, nil); !errors.Is(err, ErrBadRequest) {
		t.Errorf("Add 0: %v", err)
	}

	ticks, err := c.Ticks(ctx, day, day)
	if err != nil || len(ticks) != 1 || ticks[0].TrackID != ex || ticks[0].Date != day {
		t.Fatalf("Ticks = %+v, %v", ticks, err)
	}

	text, err := c.SetNote(ctx, ex, day, "  Finished 02:14\r\n3.2 GB ")
	if err != nil || text != "Finished 02:14\n3.2 GB" {
		t.Fatalf("SetNote = %q, %v", text, err)
	}
	notes, err := c.Notes(ctx, day.AddDays(-6), day)
	if err != nil || len(notes) != 1 || notes[0].Text != text {
		t.Fatalf("Notes = %+v, %v", notes, err)
	}
	if err := c.DeleteNote(ctx, ex, day); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Note(ex, day.String()); ok {
		t.Error("note not deleted")
	}

	s.Stats[ex] = map[string]any{"total": 212, "weeklyMean": 4.3, "current": map[string]any{"isStreak": true, "length": 3, "from": "2026-10-15", "to": "2026-10-17"}, "upToYesterday": true}
	stats, err := c.Stats(ctx, day, ex, false, false)
	if err != nil || len(stats) != 1 || stats[0].Total != 212 || stats[0].Current == nil || stats[0].Current.Length != 3 || !stats[0].UpToYesterday {
		t.Fatalf("Stats = %+v, %v", stats, err)
	}
	if got := s.Logged()[len(s.Logged())-1]; got != "GET /ocs/v2.php/apps/tickbuddy/api/stats" {
		t.Errorf("last request %q", got)
	}

	tr, err := c.CreateTrack(ctx, "Reading", TypeYesNo)
	if err != nil || tr.Name != "Reading" {
		t.Fatalf("CreateTrack = %+v, %v", tr, err)
	}
	tr, err = c.RenameTrack(ctx, tr.ID, "Books")
	if err != nil || tr.Name != "Books" {
		t.Fatalf("RenameTrack = %+v, %v", tr, err)
	}
	raw, err := c.Export(ctx, false)
	if err != nil || !json.Valid(raw) || !strings.Contains(string(raw), `"exportedAt"`) {
		t.Fatalf("Export = %s, %v", raw, err)
	}

	s.Update(ex, func(t *fakeserver.Track) { t.ArchivedAt = "2026-10-01" })
	tracks, _ := c.Tracks(ctx, false)
	all, _ := c.Tracks(ctx, true)
	if len(tracks) != 2 || len(all) != 3 {
		t.Errorf("tracks %d, with archived %d", len(tracks), len(all))
	}
	if err := c.RevokeAppPassword(ctx); err != nil || !s.Revoked {
		t.Errorf("revoke: %v", err)
	}
}

func TestNotePointer(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Nightly backup", "boolean")
	s.SetNote(ex, day.String(), "old")

	r, err := c.SetYesNo(ctx, ex, day, true, nil)
	if err != nil || r.Note != nil {
		t.Fatalf("nil note: %+v %v", r, err)
	}
	if n, _ := s.Note(ex, day.String()); n != "old" {
		t.Errorf("nil note changed the note to %q", n)
	}
	r, err = c.SetYesNo(ctx, ex, day, true, ReplaceNote("Finished 02:14"))
	if err != nil || r.Note == nil || *r.Note != "Finished 02:14" || r.Archived {
		t.Fatalf("set note: %+v %v", r, err)
	}
	r, err = c.SetYesNo(ctx, ex, day, true, ReplaceNote(""))
	if err != nil || r.Note == nil || *r.Note != "" || r.Archived {
		t.Fatalf("empty note: %+v %v", r, err)
	}
	if _, ok := s.Note(ex, day.String()); ok {
		t.Error("empty note didn't delete")
	}
}

func TestArchivedResult(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Nightly backup", "boolean")
	cof := s.AddTrack("Coffee", "counter")
	s.Update(ex, func(t *fakeserver.Track) { t.ArchivedAt = "2026-10-01" })
	s.Update(cof, func(t *fakeserver.Track) { t.ArchivedAt = "2026-10-01" })

	if r, err := c.SetYesNo(ctx, ex, day, true, nil); err != nil || !r.Archived {
		t.Errorf("SetYesNo archived: %+v %v", r, err)
	}
	if r, err := c.SetCount(ctx, cof, day, 2, nil); err != nil || !r.Archived {
		t.Errorf("SetCount archived: %+v %v", r, err)
	}
	if r, err := c.Add(ctx, cof, day, 1, nil); err != nil || r.Archived {
		t.Errorf("Add without note can't tell: %+v %v", r, err)
	}
	if r, err := c.Add(ctx, cof, day, 1, ReplaceNote("x")); err != nil || !r.Archived {
		t.Errorf("Add with note: %+v %v", r, err)
	}
	if text, err := c.SetNote(ctx, ex, day, "x"); err != nil || text != "" {
		t.Errorf("SetNote archived: %q %v", text, err)
	}
}

func TestMissingFlag(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	s.Old()
	before := len(s.Logged())

	if _, err := c.SetYesNo(ctx, ex, day, true, ReplaceNote("x")); !errors.Is(err, ErrNeedsNewerServer) {
		t.Errorf("note on old server: %v", err)
	}
	for name, f := range map[string]func() error{
		"Stats":      func() error { _, err := c.Stats(ctx, day, 0, false, false); return err },
		"Notes":      func() error { _, err := c.Notes(ctx, day, day); return err },
		"SetNote":    func() error { _, err := c.SetNote(ctx, ex, day, "x"); return err },
		"DeleteNote": func() error { return c.DeleteNote(ctx, ex, day) },
	} {
		err := f()
		var fe *FeatureError
		if !errors.Is(err, ErrNeedsNewerServer) || !errors.As(err, &fe) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, r := range s.Logged()[before:] {
		if !strings.Contains(r, "capabilities") {
			t.Errorf("sent %s despite the missing flag", r)
		}
	}
}

func TestFallbacks(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Coffee", "counter")
	s.Old()
	d := day.String()

	// booleanSet missing: read, then toggle only if needed
	if r, err := c.SetYesNo(ctx, ex, day, true, nil); err != nil || !r.Value.Bool() {
		t.Fatalf("tick: %+v %v", r, err)
	}
	if r, err := c.SetYesNo(ctx, ex, day, true, nil); err != nil || !r.Value.Bool() || s.Tick(ex, d) != 1 {
		t.Fatalf("tick again: %+v %v", r, err)
	}
	if n := s.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/toggle"); n != 1 {
		t.Errorf("toggled %d times, want 1", n)
	}
	if r, err := c.SetYesNo(ctx, ex, day, false, nil); err != nil || r.Value.Bool() || s.Tick(ex, d) != 0 {
		t.Fatalf("untick: %+v %v", r, err)
	}

	// counterIncrement missing: read, then set the sum
	s.SetTick(cof, d, 2)
	if r, err := c.Add(ctx, cof, day, 3, nil); err != nil || r.Value.Int() != 5 || s.Tick(cof, d) != 5 {
		t.Fatalf("add: %+v %v", r, err)
	}
	if s.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment") != 0 {
		t.Error("used increment without the flag")
	}

	// with the flags: no reads before writes
	s2, c2, _ := setup(t)
	ex2 := s2.AddTrack("Exercise", "boolean")
	cof2 := s2.AddTrack("Coffee", "counter")
	_, _ = c2.SetYesNo(ctx, ex2, day, true, nil)
	_, _ = c2.Add(ctx, cof2, day, 1, nil)
	if s2.Count("GET /ocs/v2.php/apps/tickbuddy/api/ticks") != 0 || s2.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/toggle") != 0 {
		t.Errorf("fallback used with flags: %v", s2.Logged())
	}
}

func TestRetries(t *testing.T) {
	s, c, waits := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Coffee", "counter")
	if _, err := c.Capabilities(ctx); err != nil {
		t.Fatal(err)
	}

	s.FailNext = 2
	if _, err := c.SetYesNo(ctx, ex, day, true, nil); err != nil {
		t.Fatalf("tick through a flaky server: %v", err)
	}
	if len(*waits) != 2 || (*waits)[0] != time.Second || (*waits)[1] != 2*time.Second {
		t.Errorf("waits = %v", *waits)
	}

	*waits = nil
	s.FailNext = 4
	if _, err := c.SetYesNo(ctx, ex, day, true, nil); !errors.Is(err, ErrUnreachable) {
		t.Errorf("4 failures: %v", err)
	}
	if len(*waits) != 3 || (*waits)[2] != 4*time.Second {
		t.Errorf("waits = %v", *waits)
	}

	// Add: never retried, also when the server applied it
	s.FailNext = 0
	before := s.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment")
	s.ApplyThenFail = 1
	if _, err := c.Add(ctx, cof, day, 1, nil); !errors.Is(err, ErrUnreachable) {
		t.Errorf("add: %v", err)
	}
	if n := s.Count("POST /ocs/v2.php/apps/tickbuddy/api/ticks/increment") - before; n != 1 {
		t.Errorf("add sent %d times", n)
	}
	if s.Tick(cof, day.String()) != 1 {
		t.Errorf("count = %d", s.Tick(cof, day.String()))
	}

	// a 4xx is never retried
	*waits = nil
	if _, err := c.SetCount(ctx, 99, day, 1, nil); !errors.Is(err, ErrTrackNotFound) || len(*waits) != 0 {
		t.Errorf("404: %v, waits %v", err, *waits)
	}

	// WithRetries
	c0 := New(Account{URL: s.URL, User: s.User, AppPassword: s.Password}, WithRetries(0))
	s.FailNext = 1
	if _, err := c0.Ticks(ctx, day, day); !errors.Is(err, ErrUnreachable) {
		t.Errorf("WithRetries(0): %v", err)
	}
}

func TestUnauthorized(t *testing.T) {
	s, _, _ := setup(t)
	c := New(Account{URL: s.URL, User: s.User, AppPassword: "wrong"})
	if _, err := c.Capabilities(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
}

func TestValueJSON(t *testing.T) {
	for _, tc := range []struct {
		v    Value
		want string
	}{{BoolValue(true), "true"}, {BoolValue(false), "false"}, {IntValue(4), "4"}, {IntValue(0), "0"}} {
		b, _ := json.Marshal(tc.v)
		if string(b) != tc.want {
			t.Errorf("%v marshals to %s", tc.v, b)
		}
		var back Value
		if err := json.Unmarshal(b, &back); err != nil || back != tc.v {
			t.Errorf("%s unmarshals to %+v, %v", b, back, err)
		}
	}
}

func TestStatsPeriod(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	s.Stats[ex] = map[string]any{"total": 212}
	from, to := Date{2025, time.January, 1}, Date{2025, time.December, 31}
	cases := []struct {
		name     string
		from, to Date
		want     map[string]string // "" for left out
	}{
		{"both", from, to, map[string]string{"from": "2025-01-01", "to": "2025-12-31"}},
		{"from alone", from, Date{}, map[string]string{"from": "2025-01-01", "to": ""}},
		{"to alone", Date{}, to, map[string]string{"from": "", "to": "2025-12-31"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stats, err := c.StatsPeriod(ctx, day, tc.from, tc.to, ex, false, false)
			if err != nil || len(stats) != 1 || stats[0].Total != 212 {
				t.Fatalf("StatsPeriod = %+v, %v", stats, err)
			}
			q := s.StatsQueries[len(s.StatsQueries)-1]
			if q.Get("today") != "2026-10-18" || q.Get("trackId") != strconv.Itoa(ex) {
				t.Errorf("query %v", q)
			}
			for k, v := range tc.want {
				if _, sent := q[k]; q.Get(k) != v || sent != (v != "") {
					t.Errorf("%s = %q (sent %v), want %q", k, q.Get(k), sent, v)
				}
			}
		})
	}

	s.Features[FeatureStatsPeriod] = false
	c = New(Account{URL: s.URL, User: s.User, AppPassword: s.Password})
	before := len(s.StatsQueries)
	_, err := c.StatsPeriod(ctx, day, from, to, ex, false, false)
	var fe *FeatureError
	if !errors.As(err, &fe) || fe.Feature != FeatureStatsPeriod {
		t.Errorf("without the flag: %v", err)
	}
	if len(s.StatsQueries) != before {
		t.Error("sent a stats request without the flag")
	}
}
