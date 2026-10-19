// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"errors"
	"fmt"
	"strings"
)

// The kinds of error this package returns. Test for them with errors.Is; the
// concrete error usually carries more detail (*Error, *FeatureError,
// *MatchError).
var (
	// ErrUnauthorized: the server answered 401, so the credentials were refused.
	ErrUnauthorized = errors.New("the server refused the credentials")
	// ErrTrackNotFound: the server answered 404, or no track matched locally.
	ErrTrackNotFound = errors.New("track not found")
	// ErrAmbiguous: a name matched several tracks.
	ErrAmbiguous = errors.New("several tracks match")
	// ErrBadRequest: the server answered 400 (or another 4xx) with its reason.
	ErrBadRequest = errors.New("the server refused the request")
	// ErrNeedsNewerServer: a feature flag this call needs is missing.
	ErrNeedsNewerServer = errors.New("this needs a newer Tickbuddy on the server")
	// ErrUnreachable: DNS, connect, TLS, timeouts and any 5xx.
	ErrUnreachable = errors.New("can't reach the server")
	// ErrNotInstalled: the server has no tickbuddy capability.
	//lint:ignore ST1005 Tickbuddy is a name
	ErrNotInstalled = errors.New("Tickbuddy isn't installed or enabled on this Nextcloud")
	// ErrUnsupportedServer: the server's apiVersion isn't one this package speaks.
	ErrUnsupportedServer = errors.New("this Tickbuddy speaks an API version this client doesn't know")
	// ErrBadDate: a date ParseDate can't read.
	ErrBadDate = errors.New("not a date")
	// ErrNoCredentials: no credentials in the environment, keychain or file.
	ErrNoCredentials = errors.New("not signed in")
)

// Error is a failed request. Kind is one of the Err* values above and
// Message is the server's own text when it sent one.
type Error struct {
	Kind    error
	Status  int // HTTP status, 0 for a network failure
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Kind.Error()
}

func (e *Error) Unwrap() error { return e.Kind }

// FeatureError is ErrNeedsNewerServer for one feature flag.
type FeatureError struct {
	Feature string
}

func (e *FeatureError) Error() string {
	return fmt.Sprintf("this needs a newer Tickbuddy on the server (it lacks %q)", e.Feature)
}

func (e *FeatureError) Is(target error) bool { return target == ErrNeedsNewerServer }

// NeedsNewerServer returns the error for a missing feature flag.
func NeedsNewerServer(feature string) error { return &FeatureError{Feature: feature} }

// MatchError is ErrTrackNotFound or ErrAmbiguous from Resolve. Candidates
// are the tracks that matched (ambiguous) or look similar (not found).
type MatchError struct {
	Kind       error
	Query      string
	Candidates []Track
}

func (e *MatchError) Error() string {
	var b strings.Builder
	if e.Kind == ErrAmbiguous {
		fmt.Fprintf(&b, "%q matches several tracks", e.Query)
	} else {
		fmt.Fprintf(&b, "no track called %q", e.Query)
	}
	if len(e.Candidates) > 0 {
		if e.Kind == ErrAmbiguous {
			b.WriteString(": ")
		} else {
			b.WriteString("; did you mean ")
		}
		for i, t := range e.Candidates {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (#%d)", t.Name, t.ID)
		}
	}
	return b.String()
}

func (e *MatchError) Unwrap() error { return e.Kind }
