// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/martinhammer/tickcli/internal/fakeserver"
)

func TestLoginFlow(t *testing.T) {
	s := fakeserver.New()
	defer s.Close()
	s.LoginPolls = 2
	ctx := context.Background()

	f, err := StartLogin(ctx, s.URL, "Tickcli on testhost", nil)
	if err != nil {
		t.Fatal(err)
	}
	if f.LoginURL != s.URL+"/index.php/login/v2/flow/abc" {
		t.Errorf("login URL %q", f.LoginURL)
	}
	a, err := f.Wait(ctx, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if a.User != "martin" || a.AppPassword != "flow-pass" || !a.CreatedByLink || a.URL != s.URL {
		t.Errorf("account %+v", a)
	}
	if n := s.Count("POST /index.php/login/v2/poll"); n != 3 {
		t.Errorf("polled %d times, want 3", n)
	}
}

func TestLoginFlowTimeout(t *testing.T) {
	s := fakeserver.New()
	defer s.Close()
	s.LoginPolls = 1 << 30
	f, err := StartLogin(context.Background(), s.URL, "Tickcli", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.Wait(ctx, 5*time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
}

func TestNormalizeURL(t *testing.T) {
	ok := map[string]string{
		"cloud.example.com":          "https://cloud.example.com",
		"https://cloud.example.com/": "https://cloud.example.com",
		"https://example.com/nc/":    "https://example.com/nc",
		"http://localhost:8080":      "http://localhost:8080",
		"http://192.168.1.10":        "http://192.168.1.10",
		"http://127.0.0.1/":          "http://127.0.0.1",
		"http://[fd00::1]:8080":      "http://[fd00::1]:8080",
	}
	for in, want := range ok {
		got, err := NormalizeURL(in, false)
		if err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"http://cloud.example.com", "http://8.8.8.8", "ftp://x", "", "https://"} {
		if _, err := NormalizeURL(in, false); err == nil {
			t.Errorf("NormalizeURL(%q) accepted", in)
		}
	}
	if got, err := NormalizeURL("http://cloud.example.com", true); err != nil || got != "http://cloud.example.com" {
		t.Errorf("--insecure-http: %q %v", got, err)
	}
}
