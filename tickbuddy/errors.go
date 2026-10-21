// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"errors"
	"fmt"
	"strings"
)

// The kinds of error this package returns. Test for them with errors.Is; the
// concrete error usually carries more detail (*Error, *FeatureError,
// *TypeError, *MatchError, *ReadOnlyError).
var (
	// ErrUnauthorized: the server answered 401, so the credentials were refused.
	ErrUnauthorized = errors.New("the server refused the credentials")
	// ErrTrackNotFound: Tickbuddy answered 404 with a reason or message, or
	// no track matched locally.
	ErrTrackNotFound = errors.New("track not found")
	// ErrAmbiguous: a name matched several tracks.
	ErrAmbiguous = errors.New("several tracks match")
	// ErrBadRequest: the server answered 400 (or another 4xx) with its reason.
	ErrBadRequest = errors.New("the server refused the request")
	// ErrNeedsNewerServer: a feature flag this call needs is missing.
	ErrNeedsNewerServer = errors.New("this needs a newer Tickbuddy on the server")
	// ErrNeedsNewerClient: the track is of a type this package doesn't know
	// (Track.KnownType), so it can be read and its notes written, but it
	// can't be ticked. Nothing was sent.
	ErrNeedsNewerClient = errors.New("this needs a newer client")
	// ErrReadOnly: the server answered 423, so the track can't be changed
	// and nothing was. Today's servers answer 200 instead (see Result.Archived).
	ErrReadOnly = errors.New("the track is read-only")
	// ErrUnreachable: DNS, connect, TLS, timeouts and any 5xx.
	ErrUnreachable = errors.New("can't reach the server")
	// ErrNotInstalled: the server has no tickbuddy capability, or answered
	// Tickbuddy's API with Nextcloud's own 404 (empty data), as while the app
	// is disabled.
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

// TypeError is ErrNeedsNewerClient for a track of type Type. A front end
// words its own message from it, naming itself.
type TypeError struct {
	Track string // the track's name
	Type  string // its type, as the server sent it
}

func (e *TypeError) Error() string {
	return fmt.Sprintf("%s is a %q track; this client needs an update to tick it", e.Track, e.Type)
}

func (e *TypeError) Is(target error) bool { return target == ErrNeedsNewerClient }

// NeedsNewerClient returns the error for writing a value to t, a track of a
// type this package doesn't know. Check before sending: the package's write
// calls take a track id, so they can't tell.
func NeedsNewerClient(t Track) error { return &TypeError{Track: t.Name, Type: t.Type} }

// Reasons a server gives for ErrReadOnly. Others may follow.
const (
	ReasonArchived  = "archived"
	ReasonImmutable = "immutable"
)

// ReadOnlyError is ErrReadOnly with the server's reason (ReasonArchived,
// ReasonImmutable, …) and its own message.
type ReadOnlyError struct {
	Reason  string
	Message string
}

func (e *ReadOnlyError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Reason != "" {
		return fmt.Sprintf("the track is read-only (%s), so nothing was changed", e.Reason)
	}
	return "the track is read-only, so nothing was changed"
}

func (e *ReadOnlyError) Is(target error) bool { return target == ErrReadOnly }

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
