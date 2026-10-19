// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/tickbuddy/tickbuddytest"
)

// TestRoundTripFreshStore writes through one Store and reads through a new
// Store and StoredCredentials on the same machine, as when tickcli stores a
// login and TickLLMe reads it.
func TestRoundTripFreshStore(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		setup func(*Store)
		want  string
	}{
		{"keychain", func(s *Store) { s.Keyring = &fakeKeyring{m: map[string]string{}} }, "keychain"},
		{"systemd-creds", func(s *Store) { s.SystemdCreds = &fakeCreds{machine: "a"} }, "file (systemd-creds, host key)"},
		{"machine-id", func(*Store) {}, "file (machine id)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := testStore(t, nil)
			tc.setup(w)
			if _, err := w.Save(ctx, acct); err != nil {
				t.Fatal(err)
			}
			r := *w // same keychain, paths and machine; no shared state
			a, src, err := NewStoredCredentials(&r).Account(ctx)
			if err != nil || a != acct || src.String() != tc.want {
				t.Fatalf("read back %+v %v %v", a, src, err)
			}
			if removed, err := (&r).Delete(ctx); !removed || err != nil {
				t.Fatalf("Delete = %v %v", removed, err)
			}
			if _, _, err := w.Load(ctx); !errors.Is(err, ErrNoCredentials) {
				t.Errorf("after Delete through the other store: %v", err)
			}
		})
	}
}

func TestSignInAndLogout(t *testing.T) {
	ctx := context.Background()
	srv := tickbuddytest.New()
	defer srv.Close()
	s := testStore(t, nil)

	a := Account{URL: srv.URL, User: "martin", AppPassword: "app-pass", CreatedByLink: true}
	caps, src, err := s.SignIn(ctx, a, WithUserAgent("Test/1"))
	if err != nil || caps.Version != "2.3.0" || src.Method != MethodMachineID {
		t.Fatalf("SignIn = %+v %v %v", caps, src, err)
	}

	st, err := ReadStatus(ctx, NewStoredCredentials(s))
	if err != nil || st.Account != a || st.Version != "2.3.0" || st.Source.String() != "file (machine id)" || st.Missing == nil || len(st.Missing) != 0 {
		t.Fatalf("ReadStatus = %+v %v", st, err)
	}

	res, err := s.Logout(ctx, false)
	if err != nil || res != (LogoutResult{SignedIn: true, Revoked: true}) || !srv.Revoked {
		t.Fatalf("Logout = %+v %v revoked=%v", res, err, srv.Revoked)
	}
	if res, err := s.Logout(ctx, false); err != nil || res.SignedIn {
		t.Errorf("second Logout = %+v %v", res, err)
	}
}

func TestSignInRefused(t *testing.T) {
	ctx := context.Background()
	srv := tickbuddytest.New()
	defer srv.Close()
	s := testStore(t, nil)
	a := Account{URL: srv.URL, User: "martin", AppPassword: "wrong", CreatedByLink: true}
	if _, _, err := s.SignIn(ctx, a); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Error("stored after a refusal")
	}
}

func TestSignInRevokesWhenNotStored(t *testing.T) {
	ctx := context.Background()
	srv := tickbuddytest.New()
	defer srv.Close()
	s := testStore(t, nil)
	s.GOOS = "darwin" // no keychain and no file: nowhere to store
	a := Account{URL: srv.URL, User: "martin", AppPassword: "app-pass", CreatedByLink: true}
	if _, _, err := s.SignIn(ctx, a); err == nil || !srv.Revoked {
		t.Errorf("err = %v, revoked = %v", err, srv.Revoked)
	}

	// a pasted app password is never revoked
	srv2 := tickbuddytest.New()
	defer srv2.Close()
	a = Account{URL: srv2.URL, User: "martin", AppPassword: "app-pass"}
	if _, _, err := s.SignIn(ctx, a); err == nil || srv2.Revoked {
		t.Errorf("pasted: err = %v, revoked = %v", err, srv2.Revoked)
	}
}

func TestLogoutPastedAndLocal(t *testing.T) {
	ctx := context.Background()
	srv := tickbuddytest.New()
	defer srv.Close()
	s := testStore(t, nil)

	if _, err := s.Save(ctx, Account{URL: srv.URL, User: "martin", AppPassword: "app-pass"}); err != nil {
		t.Fatal(err)
	}
	if res, err := s.Logout(ctx, false); err != nil || res != (LogoutResult{SignedIn: true, Pasted: true}) || srv.Revoked {
		t.Errorf("pasted: %+v %v revoked=%v", res, err, srv.Revoked)
	}

	if _, err := s.Save(ctx, Account{URL: srv.URL, User: "martin", AppPassword: "app-pass", CreatedByLink: true}); err != nil {
		t.Fatal(err)
	}
	if res, err := s.Logout(ctx, true); err != nil || res != (LogoutResult{SignedIn: true}) || srv.Revoked {
		t.Errorf("local: %+v %v revoked=%v", res, err, srv.Revoked)
	}
}

func TestLogoutKeepsWhenNotRevoked(t *testing.T) {
	ctx := context.Background()
	srv := tickbuddytest.New()
	s := testStore(t, nil)
	if _, err := s.Save(ctx, Account{URL: srv.URL, User: "martin", AppPassword: "app-pass", CreatedByLink: true}); err != nil {
		t.Fatal(err)
	}
	srv.Close() // unreachable
	_, err := s.Logout(ctx, false, WithRetries(0))
	if !errors.Is(err, ErrNotRevoked) || !errors.Is(err, ErrUnreachable) {
		t.Errorf("err = %v", err)
	}
	if _, _, err := s.LoadStored(ctx); err != nil {
		t.Errorf("forgotten although not revoked: %v", err)
	}
}

func TestLoginFlowExpires(t *testing.T) {
	defer func(d time.Duration) { loginTimeout = d }(loginTimeout)
	loginTimeout = 30 * time.Millisecond
	s := tickbuddytest.New()
	defer s.Close()
	s.LoginPolls = 1 << 30
	f, err := StartLogin(context.Background(), s.URL, "Tickcli", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Wait(context.Background(), 5*time.Millisecond); !errors.Is(err, ErrLoginExpired) {
		t.Errorf("err = %v", err)
	}
}

func TestMissing(t *testing.T) {
	c := Capabilities{Features: map[string]bool{FeatureStats: true}}
	got := c.Missing()
	if slices.Contains(got, FeatureStats) || len(got) != len(Features)-1 || got[0] != FeatureArchivedTracks {
		t.Errorf("Missing = %v", got)
	}
}
