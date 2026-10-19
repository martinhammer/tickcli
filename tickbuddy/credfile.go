// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// credFile is the credentials file. v1 is the machine-id method, v2
// systemd-creds; Method says which, so reading picks the right one.
type credFile struct {
	V      int    `json:"v"`
	Method string `json:"method"`
	TPM    bool   `json:"tpm,omitempty"`
	Nonce  string `json:"nonce,omitempty"`
	Data   string `json:"data"`
}

func (s *Store) fileSupported() bool {
	switch s.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly":
		return true
	}
	return false
}

func (s *Store) writeFile(ctx context.Context, plain []byte) (Source, error) {
	if !s.fileSupported() {
		return Source{}, errors.New("the system keychain isn't available; set the TICKBUDDY_* environment variables instead")
	}
	var f credFile
	var src Source
	if s.systemdCredsWorks(ctx) {
		sealed, err := s.SystemdCreds.Encrypt(ctx, plain)
		if err != nil {
			return Source{}, fmt.Errorf("systemd-creds couldn't encrypt the credentials: %w", err)
		}
		tpm := s.SystemdCreds.HasTPM(ctx)
		f = credFile{V: 2, Method: MethodSystemdCreds, TPM: tpm, Data: string(bytes.TrimSpace(sealed))}
		src = Source{Kind: SourceFile, Method: MethodSystemdCreds, TPM: tpm}
	} else {
		key, err := s.machineKey()
		if err != nil {
			return Source{}, err
		}
		nonce := make([]byte, 12)
		if _, err := rand.Read(nonce); err != nil {
			return Source{}, err
		}
		gcm, err := newGCM(key)
		if err != nil {
			return Source{}, err
		}
		f = credFile{
			V: 1, Method: MethodMachineID,
			Nonce: base64.StdEncoding.EncodeToString(nonce),
			Data:  base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, plain, nil)),
		}
		src = Source{Kind: SourceFile, Method: MethodMachineID}
	}
	out, err := json.Marshal(f)
	if err != nil {
		return Source{}, err
	}
	if err := writePrivate(s.Dir, s.Path(), append(out, '\n')); err != nil {
		return Source{}, err
	}
	return src, nil
}

// systemdCredsWorks encrypts and decrypts a test value.
func (s *Store) systemdCredsWorks(ctx context.Context) bool {
	if s.SystemdCreds == nil {
		return false
	}
	probe := []byte("tickcli probe")
	sealed, err := s.SystemdCreds.Encrypt(ctx, probe)
	if err != nil {
		return false
	}
	back, err := s.SystemdCreds.Decrypt(ctx, sealed)
	return err == nil && bytes.Equal(back, probe)
}

func (s *Store) readFile(ctx context.Context) (Account, Source, error) {
	raw, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return Account{}, Source{}, ErrNoCredentials
	}
	if err != nil {
		return Account{}, Source{}, err
	}
	var f credFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return Account{}, Source{}, fmt.Errorf("the credentials file %s is damaged; sign in again: %w", s.Path(), err)
	}
	var plain []byte
	src := Source{Kind: SourceFile, Method: f.Method, TPM: f.TPM}
	switch f.Method {
	case MethodSystemdCreds:
		if s.SystemdCreds == nil {
			return Account{}, Source{}, errors.New("the credentials file needs systemd-creds, which isn't available here")
		}
		plain, err = s.SystemdCreds.Decrypt(ctx, []byte(f.Data))
		if err != nil {
			return Account{}, Source{}, fmt.Errorf("systemd-creds couldn't decrypt %s (was it copied from another machine?); sign in again: %w", s.Path(), err)
		}
	case MethodMachineID:
		plain, err = s.openMachineID(f)
		if err != nil {
			return Account{}, Source{}, err
		}
	default:
		return Account{}, Source{}, fmt.Errorf("the credentials file %s uses %q, which this version doesn't know", s.Path(), f.Method)
	}
	var a Account
	if err := json.Unmarshal(plain, &a); err != nil {
		return Account{}, Source{}, fmt.Errorf("the credentials file %s is damaged; sign in again: %w", s.Path(), err)
	}
	return a, src, nil
}

func (s *Store) openMachineID(f credFile) ([]byte, error) {
	key, err := s.machineKey()
	if err != nil {
		return nil, err
	}
	nonce, err1 := base64.StdEncoding.DecodeString(f.Nonce)
	data, err2 := base64.StdEncoding.DecodeString(f.Data)
	if err1 != nil || err2 != nil {
		return nil, fmt.Errorf("the credentials file %s is damaged; sign in again", s.Path())
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, fmt.Errorf("the credentials file %s is damaged; sign in again", s.Path())
	}
	plain, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("can't decrypt %s: it was written on another machine or by another user; sign in again", s.Path())
	}
	return plain, nil
}

// machineKey is HKDF-SHA256(secret = /etc/machine-id without surrounding
// white space, salt = the numeric user id in decimal, info = "tickcli
// credentials v1").
func (s *Store) machineKey() ([]byte, error) {
	id, err := os.ReadFile(s.MachineIDPath)
	id = bytes.TrimSpace(id)
	if err != nil || len(id) == 0 {
		return nil, errors.New("no keychain and no machine id here; set the TICKBUDDY_* environment variables instead")
	}
	return hkdf.Key(sha256.New, id, []byte(strconv.Itoa(s.UID)), hkdfInfo, 32)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// writePrivate writes data to path (mode 0600) in dir (mode 0700), through a
// temporary file so a crash never leaves half a file.
func writePrivate(dir, path string, data []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Clean(path))
}

// execSystemdCreds runs per-user systemd-creds (systemd 256 or newer).
type execSystemdCreds struct{}

func (execSystemdCreds) run(ctx context.Context, in []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemd-creds", args...)
	cmd.Stdin = bytes.NewReader(in)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out.Bytes(), nil
}

func (e execSystemdCreds) Encrypt(ctx context.Context, plain []byte) ([]byte, error) {
	return e.run(ctx, plain, "--user", "encrypt", "--name="+credsName, "-", "-")
}

func (e execSystemdCreds) Decrypt(ctx context.Context, sealed []byte) ([]byte, error) {
	return e.run(ctx, sealed, "--user", "decrypt", "--name="+credsName, "-", "-")
}

func (execSystemdCreds) HasTPM(ctx context.Context) bool {
	// systemd 257 moved has-tpm2 to systemd-analyze; older ones have it here.
	for _, c := range [][]string{{"systemd-analyze", "has-tpm2"}, {"systemd-creds", "has-tpm2"}} {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := exec.CommandContext(cctx, c[0], c[1:]...).Run()
		cancel()
		if err == nil {
			return true
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false
		}
	}
	return false
}
