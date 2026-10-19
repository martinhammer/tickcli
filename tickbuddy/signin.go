// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

// SignIn, Logout and ReadStatus are the shared sign-in for every Tickbuddy
// Go client: tickcli and TickLLMe both call them, so the two agree on what
// is checked, stored and revoked, and each finds the other's login. A front
// end only adds its prompts, wording and exit codes.

import (
	"context"
	"errors"
	"fmt"
)

// SignIn checks acct against the server and, once the server accepts it,
// stores it in s. An app password the login link created (CreatedByLink) is
// revoked again when it can't be used or stored, so none is left behind
// that nothing uses. opts configure the client used for the check, such as
// WithUserAgent.
//
// The error says nothing was stored. It wraps ErrUnauthorized when the
// server refused the user and app password, and the server's error kind
// otherwise.
func (s *Store) SignIn(ctx context.Context, acct Account, opts ...Option) (Capabilities, Source, error) {
	c := New(acct, opts...)
	caps, err := c.Capabilities(ctx)
	if err != nil {
		if acct.CreatedByLink && !errors.Is(err, ErrUnauthorized) {
			_ = c.RevokeAppPassword(ctx)
		}
		return Capabilities{}, Source{}, fmt.Errorf("%w; nothing was stored", err)
	}
	src, err := s.Save(ctx, acct)
	if err != nil {
		if acct.CreatedByLink {
			_ = c.RevokeAppPassword(ctx)
		}
		return Capabilities{}, Source{}, fmt.Errorf("couldn't store the credentials: %w", err)
	}
	return caps, src, nil
}

// ErrNotRevoked: logout couldn't revoke the app password on the server, so
// it kept the stored credentials. Logging out with local set only forgets
// them.
var ErrNotRevoked = errors.New("couldn't revoke the app password on the server, so nothing was forgotten")

// LogoutResult is what Logout did.
type LogoutResult struct {
	// SignedIn is false when nothing was stored, so there was nothing to
	// forget.
	SignedIn bool
	// Revoked: the app password was revoked on the server.
	Revoked bool
	// Pasted: the app password was typed in rather than created by the
	// login link, so it was left alone and still works on the server.
	Pasted bool
}

// Logout forgets the stored credentials (not the TICKBUDDY_* environment
// variables). Unless local is set, an app password the login link created
// is revoked on the server first; one the person typed in is never revoked,
// since something else may use it. When revoking fails for any reason but
// the server already refusing it, the error wraps ErrNotRevoked and nothing
// is forgotten. opts configure the client used to revoke.
func (s *Store) Logout(ctx context.Context, local bool, opts ...Option) (LogoutResult, error) {
	acct, _, err := s.LoadStored(ctx)
	if errors.Is(err, ErrNoCredentials) {
		return LogoutResult{}, nil
	}
	// A damaged store is still forgotten, without revoking.
	res := LogoutResult{SignedIn: true, Pasted: err == nil && !acct.CreatedByLink}
	if err == nil && acct.CreatedByLink && !local {
		switch err := New(acct, opts...).RevokeAppPassword(ctx); {
		case err == nil:
			res.Revoked = true
		case errors.Is(err, ErrUnauthorized):
			// already revoked on the server
		default:
			return LogoutResult{}, fmt.Errorf("%w: %w", ErrNotRevoked, err)
		}
	}
	if _, err := s.Delete(ctx); err != nil {
		return LogoutResult{}, fmt.Errorf("couldn't forget the credentials: %w", err)
	}
	return res, nil
}

// Status is what a status command reports.
type Status struct {
	Account Account
	// Source is where the credentials came from; its String names the
	// store and how the file is encrypted.
	Source  Source
	Version string // the server's Tickbuddy version
	// Missing are the flags in Features the server lacks, in that order;
	// empty, not nil, when it has them all.
	Missing []string
}

// ReadStatus finds the credentials and asks the server for its version and
// features. opts configure the client, as for New.
func ReadStatus(ctx context.Context, creds *StoredCredentials, opts ...Option) (Status, error) {
	acct, src, err := creds.Account(ctx)
	if err != nil {
		return Status{}, err
	}
	caps, err := New(creds, opts...).Capabilities(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{Account: acct, Source: src, Version: caps.Version, Missing: caps.Missing()}, nil
}

// Missing returns the flags in Features this server lacks, in that order;
// empty, not nil, when it has them all.
func (c Capabilities) Missing() []string {
	missing := []string{}
	for _, f := range Features {
		if !c.Features[f] {
			missing = append(missing, f)
		}
	}
	return missing
}
