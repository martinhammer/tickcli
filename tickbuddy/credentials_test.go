// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeKeyring struct {
	m      map[string]string
	broken bool
}

func (k *fakeKeyring) Get(s, u string) (string, error) {
	if k.broken {
		return "", errors.New("no secret service")
	}
	v, ok := k.m[s+"/"+u]
	if !ok {
		return "", ErrKeyringNotFound
	}
	return v, nil
}

func (k *fakeKeyring) Set(s, u, v string) error {
	if k.broken {
		return errors.New("no secret service")
	}
	k.m[s+"/"+u] = v
	return nil
}

func (k *fakeKeyring) Delete(s, u string) error {
	if _, ok := k.m[s+"/"+u]; !ok {
		return ErrKeyringNotFound
	}
	delete(k.m, s+"/"+u)
	return nil
}

// fakeCreds "encrypts" by base64 with a machine marker, so another machine's
// fake can't decrypt it.
type fakeCreds struct {
	machine string
	broken  bool
	tpm     bool
}

func (f *fakeCreds) Encrypt(_ context.Context, p []byte) ([]byte, error) {
	if f.broken {
		return nil, errors.New("systemd-creds: unknown option --user")
	}
	return []byte(base64.StdEncoding.EncodeToString(append([]byte(f.machine+":"), p...))), nil
}

func (f *fakeCreds) Decrypt(_ context.Context, s []byte) ([]byte, error) {
	b, err := base64.StdEncoding.DecodeString(string(s))
	if err != nil {
		return nil, err
	}
	rest, ok := bytes.CutPrefix(b, []byte(f.machine+":"))
	if !ok {
		return nil, errors.New("wrong host key")
	}
	return rest, nil
}

func (f *fakeCreds) HasTPM(context.Context) bool { return f.tpm }

func testStore(t *testing.T, env map[string]string) *Store {
	t.Helper()
	dir := t.TempDir()
	mid := filepath.Join(dir, "machine-id")
	if err := os.WriteFile(mid, []byte("0123456789abcdef0123456789abcdef\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	return &Store{
		Getenv:        func(k string) string { return env[k] },
		Keyring:       &fakeKeyring{m: map[string]string{}, broken: true},
		Dir:           filepath.Join(dir, "config", "tickcli"),
		SystemdCreds:  nil,
		MachineIDPath: mid,
		UID:           1000,
		GOOS:          "linux",
	}
}

var acct = Account{URL: "https://cloud.example.com", User: "martin", AppPassword: "secret-pass", CreatedByLink: true}

func TestEnvironment(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, map[string]string{EnvURL: "https://x/", EnvUser: "u", EnvAppPassword: "p"})
	a, src, err := s.Load(ctx)
	if err != nil || a.URL != "https://x" || a.User != "u" || src.Kind != SourceEnvironment {
		t.Fatalf("Load = %+v %v %v", a, src, err)
	}

	s = testStore(t, map[string]string{EnvURL: "https://x", EnvAppPassword: "p"})
	_, _, err = s.Load(ctx)
	if !errors.Is(err, ErrNoCredentials) || !strings.Contains(err.Error(), "TICKBUDDY_URL and TICKBUDDY_APP_PASSWORD are set but TICKBUDDY_USER isn't") {
		t.Errorf("two of three: %v", err)
	}

	s = testStore(t, map[string]string{EnvURL: "https://x"})
	_, _, err = s.Load(ctx)
	if !strings.Contains(err.Error(), "TICKBUDDY_URL is set but TICKBUDDY_USER and TICKBUDDY_APP_PASSWORD aren't") {
		t.Errorf("one of three: %v", err)
	}

	s = testStore(t, nil)
	if _, _, err := s.Load(ctx); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("nothing: %v", err)
	}
}

func TestKeychain(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, nil)
	kr := &fakeKeyring{m: map[string]string{}}
	s.Keyring = kr
	src, err := s.Save(ctx, acct)
	if err != nil || src.Kind != SourceKeychain {
		t.Fatalf("Save = %v %v", src, err)
	}
	if _, ok := kr.m["tickcli/probe"]; ok {
		t.Error("probe left behind")
	}
	if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
		t.Error("file written although the keychain works")
	}
	a, src, err := s.Load(ctx)
	if err != nil || a != acct || src.String() != "keychain" {
		t.Fatalf("Load = %+v %v %v", a, src, err)
	}
	if removed, err := s.Delete(ctx); !removed || err != nil {
		t.Errorf("Delete = %v %v", removed, err)
	}
	if _, _, err := s.Load(ctx); !errors.Is(err, ErrNoCredentials) {
		t.Errorf("after Delete: %v", err)
	}
}

func TestFileMachineID(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, nil)
	src, err := s.Save(ctx, acct)
	if err != nil || src.Method != MethodMachineID || src.String() != "file (machine id)" {
		t.Fatalf("Save = %v %v", src, err)
	}
	fi, err := os.Stat(s.Path())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v %v", fi.Mode(), err)
	}
	di, _ := os.Stat(s.Dir)
	if di.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", di.Mode())
	}
	raw, _ := os.ReadFile(s.Path())
	if bytes.Contains(raw, []byte("secret-pass")) {
		t.Fatal("password in clear")
	}
	var f map[string]any
	_ = json.Unmarshal(raw, &f)
	if f["v"] != 1.0 || f["method"] != "machine-id" || f["nonce"] == nil {
		t.Errorf("file = %s", raw)
	}
	a, src, err := s.Load(ctx)
	if err != nil || a != acct || src.Kind != SourceFile {
		t.Fatalf("Load = %+v %v %v", a, src, err)
	}

	// a copy on another machine fails
	other := testStore(t, nil)
	_ = os.Remove(other.MachineIDPath)
	if err := os.WriteFile(other.MachineIDPath, []byte("ffffffffffffffffffffffffffffffff\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(other.Dir, 0o700)
	_ = os.WriteFile(other.Path(), raw, 0o600)
	if _, _, err := other.Load(ctx); err == nil || !strings.Contains(err.Error(), "another machine") {
		t.Errorf("copied file: %v", err)
	}
	// and as another user
	s.UID = 1001
	if _, _, err := s.Load(ctx); err == nil {
		t.Error("another user could read it")
	}

	// no machine id: refuses
	s = testStore(t, nil)
	s.MachineIDPath = filepath.Join(t.TempDir(), "missing")
	if _, err := s.Save(ctx, acct); err == nil || !strings.Contains(err.Error(), "no keychain and no machine id") {
		t.Errorf("no machine id: %v", err)
	}
}

func TestFileSystemdCreds(t *testing.T) {
	ctx := context.Background()
	s := testStore(t, nil)
	s.SystemdCreds = &fakeCreds{machine: "a", tpm: true}
	src, err := s.Save(ctx, acct)
	if err != nil || src.String() != "file (systemd-creds, TPM)" {
		t.Fatalf("Save = %v %v", src, err)
	}
	raw, _ := os.ReadFile(s.Path())
	var f map[string]any
	_ = json.Unmarshal(raw, &f)
	if f["v"] != 2.0 || f["method"] != "systemd-creds" {
		t.Errorf("file = %s", raw)
	}
	a, src, err := s.Load(ctx)
	if err != nil || a != acct || src.String() != "file (systemd-creds, TPM)" {
		t.Fatalf("Load = %+v %v %v", a, src, err)
	}

	s.SystemdCreds = &fakeCreds{machine: "a"}
	if src, _ := s.Save(ctx, acct); src.String() != "file (systemd-creds, host key)" {
		t.Errorf("host key: %v", src)
	}

	// another machine's systemd-creds can't read it
	s.SystemdCreds = &fakeCreds{machine: "b"}
	if _, _, err := s.Load(ctx); err == nil {
		t.Error("another machine decrypted it")
	}

	// broken systemd-creds: machine-id key instead
	s.SystemdCreds = &fakeCreds{machine: "a", broken: true}
	if src, err := s.Save(ctx, acct); err != nil || src.Method != MethodMachineID {
		t.Errorf("broken systemd-creds: %v %v", src, err)
	}
}

func TestFileNotOnMac(t *testing.T) {
	s := testStore(t, nil)
	s.GOOS = "darwin"
	if _, err := s.Save(context.Background(), acct); err == nil {
		t.Error("file used on darwin")
	}
}

func TestStoredCredentialsCaches(t *testing.T) {
	n := 0
	s := testStore(t, nil)
	s.Getenv = func(k string) string {
		n++
		return map[string]string{EnvURL: "https://x", EnvUser: "u", EnvAppPassword: "p"}[k]
	}
	c := NewStoredCredentials(s)
	for range 3 {
		if _, _, _, err := c.Get(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if n != 3 {
		t.Errorf("looked up %d times", n/3)
	}
}
