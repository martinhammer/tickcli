// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

// TestStrictHeader: every request to Tickbuddy's API asks for strict
// handling; capabilities, app password and Login Flow requests don't.
func TestStrictHeader(t *testing.T) {
	s, c, _ := setup(t)
	ctx := context.Background()
	ex := s.AddTrack("Exercise", "boolean")
	cof := s.AddTrack("Coffee", "counter")

	_, _ = c.Tracks(ctx, true)
	_, _ = c.Ticks(ctx, day, day)
	_, _ = c.SetYesNo(ctx, ex, day, true, nil)
	_, _ = c.SetCount(ctx, cof, day, 2, nil)
	_, _ = c.Add(ctx, cof, day, 1, AppendNote("x"))
	_, _ = c.SetNote(ctx, ex, day, "x")
	_, _ = c.AppendNote(ctx, ex, day, "y", false)
	_ = c.DeleteNote(ctx, ex, day)
	_, _ = c.Notes(ctx, day, day)
	_, _ = c.Stats(ctx, day, 0, false, false)
	_, _ = c.RenameTrack(ctx, ex, "Walk")
	_, _ = c.CreateTrack(ctx, "Reading", TypeYesNo)
	_, _ = c.Export(ctx, false)
	_ = c.RevokeAppPassword(ctx)
	f, err := StartLogin(ctx, s.URL, "Tickcli", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wait(ctx, time.Millisecond); err != nil {
		t.Fatal(err)
	}

	logged, prefers := s.Logged(), s.LoggedPrefers()
	api := 0
	for i, r := range logged {
		want := ""
		if strings.Contains(r, "/apps/tickbuddy/api/") {
			want = "handling=strict"
			api++
		}
		if prefers[i] != want {
			t.Errorf("%s: Prefer %q, want %q", r, prefers[i], want)
		}
	}
	if api < 13 {
		t.Errorf("only %d API requests in %v", api, logged)
	}
}

// TestReadOnly: the server's 423 is ErrReadOnly with its reason and
// message, for every write, and never retried.
func TestReadOnly(t *testing.T) {
	for _, reason := range []string{ReasonArchived, ReasonImmutable} {
		t.Run(reason, func(t *testing.T) {
			s, c, waits := setup(t)
			ctx := context.Background()
			ex := s.AddTrack("Nightly backup", "boolean")
			cof := s.AddTrack("Coffee", "counter")
			s.Update(ex, func(t *tickbuddytest.Track) { t.ReadOnly = reason })
			s.Update(cof, func(t *tickbuddytest.Track) { t.ReadOnly = reason })
			if _, err := c.Capabilities(ctx); err != nil {
				t.Fatal(err)
			}

			calls := map[string]func() error{
				"SetYesNo":    func() error { _, err := c.SetYesNo(ctx, ex, day, true, ReplaceNote("x")); return err },
				"SetCount":    func() error { _, err := c.SetCount(ctx, cof, day, 2, nil); return err },
				"Add":         func() error { _, err := c.Add(ctx, cof, day, 1, nil); return err },
				"SetNote":     func() error { _, err := c.SetNote(ctx, ex, day, "x"); return err },
				"AppendNote":  func() error { _, err := c.AppendNote(ctx, ex, day, "x", false); return err },
				"DeleteNote":  func() error { return c.DeleteNote(ctx, ex, day) },
				"RenameTrack": func() error { _, err := c.RenameTrack(ctx, ex, "Backup"); return err },
			}
			for name, call := range calls {
				before := len(s.Logged())
				err := call()
				var e *ReadOnlyError
				if !errors.Is(err, ErrReadOnly) || !errors.As(err, &e) {
					t.Errorf("%s: %v", name, err)
					continue
				}
				want := "is " + reason + ", so nothing was changed"
				if e.Reason != reason || !strings.HasSuffix(e.Message, want) || err.Error() != e.Message {
					t.Errorf("%s: %+v", name, e)
				}
				if n := len(s.Logged()) - before; n != 1 {
					t.Errorf("%s: sent %d requests", name, n)
				}
			}
			if len(*waits) != 0 {
				t.Errorf("retried: waits %v", *waits)
			}
			if s.Tick(ex, day.String()) != 0 || s.Tick(cof, day.String()) != 0 {
				t.Error("wrote despite 423")
			}
		})
	}
}

// TestLockedElsewhere: a 423 from outside Tickbuddy's API isn't
// ErrReadOnly, and a 423 without a reason still is.
func TestLockedElsewhere(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(`{"ocs":{"meta":{"message":"Locked"},"data":{"message":"File is locked","reason":"archived"}}}`))
	}))
	defer srv.Close()
	c := New(Account{URL: srv.URL, User: "u", AppPassword: "p"}, WithRetries(0))
	if _, err := c.Capabilities(context.Background()); errors.Is(err, ErrReadOnly) || !errors.Is(err, ErrBadRequest) {
		t.Errorf("capabilities 423: %v", err)
	}
	if err := c.RevokeAppPassword(context.Background()); errors.Is(err, ErrReadOnly) || !errors.Is(err, ErrBadRequest) {
		t.Errorf("app password 423: %v", err)
	}

	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusLocked)
	}))
	defer bare.Close()
	c = New(Account{URL: bare.URL, User: "u", AppPassword: "p"}, WithRetries(0))
	_, err := c.RenameTrack(context.Background(), 1, "x")
	var e *ReadOnlyError
	if !errors.As(err, &e) || e.Reason != "" || err.Error() != "the track is read-only, so nothing was changed" {
		t.Errorf("bare 423: %v", err)
	}
}
