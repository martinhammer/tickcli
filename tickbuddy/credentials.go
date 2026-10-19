// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/zalando/go-keyring"
)

// Credentials supplies the server URL, user and app password for each
// request. StoredCredentials is the implementation tickcli uses; Account is
// a fixed one; a remote server can supply its own per-user implementation.
type Credentials interface {
	Get(ctx context.Context) (url, user, password string, err error)
}

// Account is one set of sign-in details. It is also a Credentials that
// always returns itself.
type Account struct {
	URL         string `json:"url"`
	User        string `json:"user"`
	AppPassword string `json:"appPassword"`
	// CreatedByLink is set when the app password came from Login Flow v2,
	// so logging out may revoke it on the server.
	CreatedByLink bool `json:"createdByLink"`
}

// Get returns the account's details.
func (a Account) Get(context.Context) (string, string, string, error) {
	return a.URL, a.User, a.AppPassword, nil
}

// Names shared with TickLLMe, which reads the same keychain entry and file.
const (
	KeyringService  = "tickcli"
	KeyringUser     = "default"
	credentialsFile = "credentials"
	credsName       = "tickcli-credentials"
	hkdfInfo        = "tickcli credentials v1"
)

// Environment variables that supply credentials for a single run.
const (
	EnvURL         = "TICKBUDDY_URL"
	EnvUser        = "TICKBUDDY_USER"
	EnvAppPassword = "TICKBUDDY_APP_PASSWORD"
)

// Where credentials came from.
const (
	SourceEnvironment = "environment"
	SourceKeychain    = "keychain"
	SourceFile        = "file"
)

// How the credentials file is encrypted.
const (
	MethodSystemdCreds = "systemd-creds"
	MethodMachineID    = "machine-id"
)

// Source says where credentials were found or stored.
type Source struct {
	Kind   string // SourceEnvironment, SourceKeychain or SourceFile
	Method string // for a file: MethodSystemdCreds or MethodMachineID
	TPM    bool   // for systemd-creds: bound to the TPM rather than the host key
}

// String is how tickcli status names the source: "keychain",
// "environment", "file (systemd-creds, TPM)", "file (systemd-creds, host
// key)" or "file (machine id)".
func (s Source) String() string {
	switch {
	case s.Kind != SourceFile:
		return s.Kind
	case s.Method == MethodSystemdCreds && s.TPM:
		return "file (systemd-creds, TPM)"
	case s.Method == MethodSystemdCreds:
		return "file (systemd-creds, host key)"
	default:
		return "file (machine id)"
	}
}

// Keyring is the OS keychain. ErrKeyringNotFound means there is no entry.
type Keyring interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

// ErrKeyringNotFound is what a Keyring returns for a missing entry.
var ErrKeyringNotFound = keyring.ErrNotFound

// SystemdCreds encrypts and decrypts with per-user systemd-creds.
type SystemdCreds interface {
	Encrypt(ctx context.Context, plain []byte) ([]byte, error)
	Decrypt(ctx context.Context, sealed []byte) ([]byte, error)
	// HasTPM reports whether the machine has a usable TPM2, so that
	// systemd-creds binds to it rather than to the host key alone.
	HasTPM(ctx context.Context) bool
}

// Store finds and keeps credentials: the TICKBUDDY_* environment variables,
// then the OS keychain, then an encrypted file. Its fields are the
// machine's parts, replaceable in tests; DefaultStore fills them in.
type Store struct {
	Getenv        func(string) string
	Keyring       Keyring      // nil when there is no keychain
	Dir           string       // directory of the credentials file
	SystemdCreds  SystemdCreds // nil when systemd-creds is unavailable
	MachineIDPath string
	UID           int
	GOOS          string
}

// DefaultStore is the store on this machine. Its paths are fixed, since
// tickcli and TickLLMe share them.
func DefaultStore() *Store {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return &Store{
		Getenv:        os.Getenv,
		Keyring:       timeoutKeyring{d: 5 * time.Second},
		Dir:           filepath.Join(dir, "tickcli"),
		SystemdCreds:  execSystemdCreds{},
		MachineIDPath: "/etc/machine-id",
		UID:           os.Getuid(),
		GOOS:          runtime.GOOS,
	}
}

// Path is the credentials file.
func (s *Store) Path() string { return filepath.Join(s.Dir, credentialsFile) }

// FromEnvironment returns the account the TICKBUDDY_* variables name. ok is
// false when none is set; setting some but not all three is an error that
// names the missing ones.
func (s *Store) FromEnvironment() (a Account, ok bool, err error) {
	vals := map[string]string{}
	var missing []string
	for _, k := range []string{EnvURL, EnvUser, EnvAppPassword} {
		v := s.Getenv(k)
		vals[k] = v
		if v == "" {
			missing = append(missing, k)
		}
	}
	switch len(missing) {
	case 3:
		return Account{}, false, nil
	case 0:
		return Account{URL: strings.TrimRight(vals[EnvURL], "/"), User: vals[EnvUser], AppPassword: vals[EnvAppPassword]}, true, nil
	}
	set := setNames(vals)
	verb, verbNot := "is", "isn't"
	if strings.Contains(set, " and ") {
		verb = "are"
	}
	if len(missing) > 1 {
		verbNot = "aren't"
	}
	return Account{}, false, fmt.Errorf("%w: %s %s set but %s %s; set all three or none", ErrNoCredentials, set, verb, strings.Join(missing, " and "), verbNot)
}

func setNames(vals map[string]string) string {
	var set []string
	for _, k := range []string{EnvURL, EnvUser, EnvAppPassword} {
		if vals[k] != "" {
			set = append(set, k)
		}
	}
	return strings.Join(set, " and ")
}

// Load finds credentials: environment, then keychain, then file. Without
// any it fails with ErrNoCredentials.
func (s *Store) Load(ctx context.Context) (Account, Source, error) {
	a, ok, err := s.FromEnvironment()
	if err != nil {
		return Account{}, Source{}, err
	}
	if ok {
		return a, Source{Kind: SourceEnvironment}, nil
	}
	return s.LoadStored(ctx)
}

// LoadStored is Load without the environment: what login stored.
func (s *Store) LoadStored(ctx context.Context) (Account, Source, error) {
	if s.Keyring != nil {
		secret, err := s.Keyring.Get(KeyringService, KeyringUser)
		if err == nil {
			var a Account
			if err := json.Unmarshal([]byte(secret), &a); err != nil {
				return Account{}, Source{}, fmt.Errorf("the keychain entry %q is damaged; sign in again: %w", KeyringService, err)
			}
			return a, Source{Kind: SourceKeychain}, nil
		}
	}
	return s.readFile(ctx)
}

// Save stores a in the keychain when a test write and read succeed there,
// otherwise in the encrypted file (Linux and the BSDs only), and removes any
// copy from the other place.
func (s *Store) Save(ctx context.Context, a Account) (Source, error) {
	plain, err := json.Marshal(a)
	if err != nil {
		return Source{}, err
	}
	if s.keyringWorks() {
		if err := s.Keyring.Set(KeyringService, KeyringUser, string(plain)); err == nil {
			if err := os.Remove(s.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Source{}, fmt.Errorf("stored in the keychain, but couldn't remove the old file %s: %w", s.Path(), err)
			}
			return Source{Kind: SourceKeychain}, nil
		}
	}
	return s.writeFile(ctx, plain)
}

func (s *Store) keyringWorks() bool {
	if s.Keyring == nil {
		return false
	}
	const probeUser = "probe"
	probe := fmt.Sprintf("probe-%d", time.Now().UnixNano())
	if err := s.Keyring.Set(KeyringService, probeUser, probe); err != nil {
		return false
	}
	got, err := s.Keyring.Get(KeyringService, probeUser)
	_ = s.Keyring.Delete(KeyringService, probeUser)
	return err == nil && got == probe
}

// Delete forgets the stored credentials (keychain entry and file). It
// reports whether there was anything to forget.
func (s *Store) Delete(ctx context.Context) (bool, error) {
	removed := false
	if s.Keyring != nil {
		if err := s.Keyring.Delete(KeyringService, KeyringUser); err == nil {
			removed = true
		}
	}
	err := os.Remove(s.Path())
	switch {
	case err == nil:
		removed = true
	case !errors.Is(err, os.ErrNotExist):
		return removed, err
	}
	return removed, nil
}

// StoredCredentials looks credentials up in a Store on first use and keeps
// them for the rest of the run.
type StoredCredentials struct {
	store *Store
	once  sync.Once
	acct  Account
	src   Source
	err   error
}

// NewStoredCredentials returns credentials from store.
func NewStoredCredentials(store *Store) *StoredCredentials {
	return &StoredCredentials{store: store}
}

func (c *StoredCredentials) load(ctx context.Context) {
	c.once.Do(func() { c.acct, c.src, c.err = c.store.Load(ctx) })
}

// Get implements Credentials.
func (c *StoredCredentials) Get(ctx context.Context) (string, string, string, error) {
	c.load(ctx)
	if c.err != nil {
		return "", "", "", c.err
	}
	return c.acct.URL, c.acct.User, c.acct.AppPassword, nil
}

// Account returns the credentials found and where they came from.
func (c *StoredCredentials) Account(ctx context.Context) (Account, Source, error) {
	c.load(ctx)
	return c.acct, c.src, c.err
}

// timeoutKeyring is go-keyring with a time limit: on a headless Linux box a
// D-Bus call to a Secret Service that isn't there can hang.
type timeoutKeyring struct{ d time.Duration }

var errKeyringTimeout = errors.New("the keychain didn't answer")

func (k timeoutKeyring) run(f func() error) error {
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		return err
	case <-time.After(k.d):
		return errKeyringTimeout
	}
}

func (k timeoutKeyring) Get(service, user string) (string, error) {
	var s string
	err := k.run(func() (err error) { s, err = keyring.Get(service, user); return })
	return s, err
}

func (k timeoutKeyring) Set(service, user, secret string) error {
	return k.run(func() error { return keyring.Set(service, user, secret) })
}

func (k timeoutKeyring) Delete(service, user string) error {
	return k.run(func() error { return keyring.Delete(service, user) })
}
