// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRealSystemdCreds round-trips through this machine's systemd-creds.
// Run it on a systemd 256+ box with TICKCLI_SYSTEMD_TEST=1.
func TestRealSystemdCreds(t *testing.T) {
	if os.Getenv("TICKCLI_SYSTEMD_TEST") == "" {
		t.Skip("set TICKCLI_SYSTEMD_TEST=1 to use the real systemd-creds")
	}
	ctx := context.Background()
	s := DefaultStore()
	s.Keyring = nil
	s.Dir = filepath.Join(t.TempDir(), "tickcli")
	src, err := s.Save(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	if src.Method != MethodSystemdCreds {
		t.Fatalf("stored with %v; systemd-creds didn't work here", src)
	}
	a, src2, err := s.Load(ctx)
	if err != nil || a != acct || src2 != src {
		t.Fatalf("Load = %+v %v %v", a, src2, err)
	}
	t.Logf("stored as %v", src)
}
