// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	apiPrefix           = "/ocs/v2.php/apps/tickbuddy/api"
	capabilitiesPath    = "/ocs/v2.php/cloud/capabilities"
	appPasswordPath     = "/ocs/v2.php/core/apppassword"
	maxResponseBytes    = 64 << 20
	defaultTimeout      = 15 * time.Second
	defaultRetries      = 3
	defaultUserAgent    = "tickbuddy-go"
	supportedAPIVersion = 1
)

// Client talks to one Tickbuddy server. It is safe for concurrent use.
type Client struct {
	creds     Credentials
	http      *http.Client
	userAgent string
	retries   int
	sleep     func(context.Context, time.Duration) error

	capsMu sync.Mutex
	caps   *Capabilities
}

// Option configures a Client.
type Option func(*Client)

// WithTimeout sets the timeout of each HTTP request (default 15 s).
func WithTimeout(d time.Duration) Option { return func(c *Client) { c.http.Timeout = d } }

// WithUserAgent sets the User-Agent header, for example "Tickcli/0.1.0".
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithRetries sets how often a call that is safe to repeat is retried after
// ErrUnreachable (default 3, waiting 1 s, 2 s, 4 s, …). Add is never retried.
func WithRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.retries = n
		}
	}
}

// WithHTTPClient replaces the HTTP client (its Timeout is kept as given).
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// New returns a client that signs in with creds.
func New(creds Credentials, opts ...Option) *Client {
	c := &Client{
		creds:     creds,
		http:      &http.Client{Timeout: defaultTimeout},
		userAgent: defaultUserAgent,
		retries:   defaultRetries,
		sleep:     sleepCtx,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// request describes one call. path is under the Tickbuddy API unless it
// starts with /ocs/ already.
type request struct {
	method string
	path   string
	query  url.Values
	body   any
	// retry marks a call that is safe to repeat (it sets a state or reads).
	retry bool
	// retryConflict also retries a 409, which POST /notes/append answers
	// when other writers kept changing the note.
	retryConflict bool
}

// envelope is the OCS wrapper around every answer.
type envelope struct {
	OCS struct {
		Meta struct {
			Message string `json:"message"`
		} `json:"meta"`
		Data json.RawMessage `json:"data"`
	} `json:"ocs"`
}

func (c *Client) do(ctx context.Context, r request, out any) error {
	base, user, password, err := c.creds.Get(ctx)
	if err != nil {
		return err
	}
	var body []byte
	if r.body != nil {
		if body, err = json.Marshal(r.body); err != nil {
			return err
		}
	}
	path := r.path
	api := !strings.HasPrefix(path, "/ocs/")
	if api {
		path = apiPrefix + path
	}
	u := strings.TrimRight(base, "/") + path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}

	attempts := 1
	if r.retry {
		attempts += c.retries
	}
	for attempt := 0; ; attempt++ {
		err = c.once(ctx, r.method, u, api, user, password, body, out)
		if err == nil || attempt+1 >= attempts || !(errors.Is(err, ErrUnreachable) || r.retryConflict && isConflict(err)) || ctx.Err() != nil {
			return err
		}
		wait := time.Second << min(attempt, 5)
		if serr := c.sleep(ctx, wait); serr != nil {
			return err
		}
	}
}

// once sends one request. api marks Tickbuddy's own API, which is asked to
// refuse writes it can't make (Prefer: handling=strict, RFC 7240) and whose
// 423 is read as ErrReadOnly.
func (c *Client) once(ctx context.Context, method, u string, api bool, user, password string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.SetBasicAuth(user, password)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if api {
		req.Header.Set("Prefer", "handling=strict")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return &Error{Kind: ErrUnreachable, Message: "can't reach the server: " + transportReason(err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return &Error{Kind: ErrUnreachable, Status: resp.StatusCode, Message: "the server's answer broke off: " + err.Error()}
	}

	var env envelope
	jsonErr := error(nil)
	if len(bytes.TrimSpace(data)) > 0 {
		jsonErr = json.Unmarshal(data, &env)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil || resp.StatusCode == http.StatusNoContent {
			return nil
		}
		if jsonErr != nil || env.OCS.Data == nil {
			return &Error{Kind: ErrUnreachable, Status: resp.StatusCode, Message: "the server's answer isn't the JSON Tickbuddy sends; is this a Nextcloud URL?"}
		}
		if raw, ok := out.(*json.RawMessage); ok {
			*raw = append((*raw)[:0], env.OCS.Data...)
			return nil
		}
		if err := json.Unmarshal(env.OCS.Data, out); err != nil {
			return fmt.Errorf("tickbuddy: unexpected answer from %s: %w", req.URL.Path, err)
		}
		return nil
	}

	msg := serverMessage(env)
	if api && resp.StatusCode == http.StatusLocked {
		var d struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(env.OCS.Data, &d)
		return &ReadOnlyError{Reason: d.Reason, Message: msg}
	}
	e := &Error{Status: resp.StatusCode, Message: msg}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		e.Kind = ErrUnauthorized
		e.Message = ""
	case resp.StatusCode == http.StatusNotFound:
		e.Kind = ErrTrackNotFound
	case resp.StatusCode >= 500:
		e.Kind = ErrUnreachable
		if msg == "" {
			e.Message = fmt.Sprintf("the server answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	default:
		e.Kind = ErrBadRequest
		if msg == "" {
			e.Message = fmt.Sprintf("the server refused the request (%d %s)", resp.StatusCode, http.StatusText(resp.StatusCode))
		}
	}
	return e
}

// isConflict reports whether err is the server's 409.
func isConflict(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusConflict
}

func serverMessage(env envelope) string {
	var d struct {
		Message string `json:"message"`
	}
	if len(env.OCS.Data) > 0 && json.Unmarshal(env.OCS.Data, &d) == nil && d.Message != "" {
		return d.Message
	}
	return env.OCS.Meta.Message
}

// transportReason shortens a *url.Error to its cause, keeping the host.
func transportReason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Timeout() {
			return "the request timed out"
		}
		return ue.Err.Error()
	}
	return err.Error()
}

// Capabilities is what the server advertises under tickbuddy.
type Capabilities struct {
	Version    string          `json:"version"`
	APIVersion int             `json:"apiVersion"`
	Features   map[string]bool `json:"features"`
}

// Feature flags this package uses.
const (
	FeatureBooleanSet       = "booleanSet"
	FeatureCounterIncrement = "counterIncrement"
	FeatureStats            = "stats"
	FeatureArchivedTracks   = "archivedTracks"
	FeatureNotes            = "notes"
	FeatureStatsPeriod      = "statsPeriod"
	FeatureNotesAppend      = "notesAppend"
)

// Features lists the flags above, in the order status messages show them.
var Features = []string{FeatureArchivedTracks, FeatureBooleanSet, FeatureCounterIncrement, FeatureStats, FeatureStatsPeriod, FeatureNotes, FeatureNotesAppend}

// Capabilities reads the server's tickbuddy capabilities once per client and
// caches them. It fails with ErrNotInstalled when the server has no
// Tickbuddy, and ErrUnsupportedServer for an apiVersion other than 1.
func (c *Client) Capabilities(ctx context.Context) (Capabilities, error) {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	if c.caps != nil {
		return *c.caps, nil
	}
	var data struct {
		Capabilities struct {
			Tickbuddy *struct {
				Version    string          `json:"version"`
				APIVersion int             `json:"apiVersion"`
				Features   json.RawMessage `json:"features"`
			} `json:"tickbuddy"`
		} `json:"capabilities"`
	}
	if err := c.do(ctx, request{method: http.MethodGet, path: capabilitiesPath, retry: true}, &data); err != nil {
		var e *Error
		if errors.As(err, &e) && e.Kind == ErrTrackNotFound {
			return Capabilities{}, &Error{Kind: ErrNotInstalled, Status: e.Status, Message: "no Nextcloud capabilities endpoint here; is this a Nextcloud URL?"}
		}
		return Capabilities{}, err
	}
	tb := data.Capabilities.Tickbuddy
	if tb == nil {
		return Capabilities{}, ErrNotInstalled
	}
	caps := Capabilities{Version: tb.Version, APIVersion: tb.APIVersion, Features: map[string]bool{}}
	// PHP sends an empty map as [], so tolerate anything that isn't a map.
	_ = json.Unmarshal(tb.Features, &caps.Features)
	if caps.Features == nil {
		caps.Features = map[string]bool{}
	}
	if caps.APIVersion != supportedAPIVersion {
		return Capabilities{}, &Error{Kind: ErrUnsupportedServer, Message: fmt.Sprintf("this Tickbuddy (%s) speaks API version %d; this client knows version %d", caps.Version, caps.APIVersion, supportedAPIVersion)}
	}
	c.caps = &caps
	return caps, nil
}

// Has reports whether the server advertises feature. It is false until
// Capabilities has succeeded once.
func (c *Client) Has(feature string) bool {
	c.capsMu.Lock()
	defer c.capsMu.Unlock()
	return c.caps != nil && c.caps.Features[feature]
}

// has reads the capabilities first if needed.
func (c *Client) has(ctx context.Context, feature string) (bool, error) {
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return false, err
	}
	return caps.Features[feature], nil
}

// need fails with ErrNeedsNewerServer unless the server has feature.
func (c *Client) need(ctx context.Context, feature string) error {
	ok, err := c.has(ctx, feature)
	if err != nil {
		return err
	}
	if !ok {
		return NeedsNewerServer(feature)
	}
	return nil
}

// RevokeAppPassword deletes the app password the client signs in with, so it
// leaves the account's Devices & sessions. Nextcloud refuses this for a
// normal password.
func (c *Client) RevokeAppPassword(ctx context.Context) error {
	return c.do(ctx, request{method: http.MethodDelete, path: appPasswordPath}, nil)
}
