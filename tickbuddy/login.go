// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LoginTimeout is how long the server keeps a Login Flow v2 open.
const LoginTimeout = 20 * time.Minute

// NormalizeURL turns what a person typed into a server URL: https:// is added
// when there is no scheme, and a trailing slash is dropped. Plain http:// is
// refused unless the host is localhost or a private address, or
// allowInsecure is set, since the app password travels with every request.
func NormalizeURL(raw string, allowInsecure bool) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("no server URL given")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q isn't a server URL", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !allowInsecure && !isLocalHost(u.Hostname()) {
			return "", fmt.Errorf("%s uses plain http, where anyone on the way can read the app password; use https, or pass --insecure-http if you must", u.Host)
		}
	default:
		return "", fmt.Errorf("%q isn't an http or https URL", raw)
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func isLocalHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// LoginFlow is a started Nextcloud Login Flow v2.
type LoginFlow struct {
	// LoginURL is the link the person opens to grant access.
	LoginURL string

	serverURL string
	token     string
	endpoint  string
	http      *http.Client
	userAgent string
}

// StartLogin starts Login Flow v2 on serverURL (already normalised). The
// app password it creates is named after userAgent in Nextcloud's Devices
// & sessions, so pass something like "Tickcli on myhost". httpClient may be
// nil.
func StartLogin(ctx context.Context, serverURL, userAgent string, httpClient *http.Client) (*LoginFlow, error) {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	f := &LoginFlow{serverURL: serverURL, http: httpClient, userAgent: userAgent}
	var start struct {
		Poll struct {
			Token    string `json:"token"`
			Endpoint string `json:"endpoint"`
		} `json:"poll"`
		Login string `json:"login"`
	}
	status, body, err := f.post(ctx, strings.TrimRight(serverURL, "/")+"/index.php/login/v2", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || json.Unmarshal(body, &start) != nil || start.Login == "" || start.Poll.Endpoint == "" {
		return nil, &Error{Kind: ErrNotInstalled, Status: status, Message: fmt.Sprintf("%s didn't start a Nextcloud sign-in (answered %d); is this a Nextcloud URL?", serverURL, status)}
	}
	f.LoginURL, f.token, f.endpoint = start.Login, start.Poll.Token, start.Poll.Endpoint
	return f, nil
}

// Wait polls every interval until the person has granted access, ctx ends
// (give it LoginTimeout at most) or the server refuses. A network failure
// while polling is tried again at the next interval.
func (f *LoginFlow) Wait(ctx context.Context, interval time.Duration) (Account, error) {
	form := url.Values{"token": {f.token}}
	for {
		status, body, err := f.post(ctx, f.endpoint, form)
		switch {
		case ctx.Err() != nil:
			return Account{}, ctx.Err()
		case err != nil, status == http.StatusNotFound, status >= 500:
			// not yet, or a hiccup
		case status == http.StatusOK:
			var done struct {
				Server      string `json:"server"`
				LoginName   string `json:"loginName"`
				AppPassword string `json:"appPassword"`
			}
			if err := json.Unmarshal(body, &done); err != nil || done.LoginName == "" || done.AppPassword == "" {
				return Account{}, errors.New("the server's sign-in answer is missing the user or app password")
			}
			return Account{URL: f.serverURL, User: done.LoginName, AppPassword: done.AppPassword, CreatedByLink: true}, nil
		default:
			return Account{}, &Error{Kind: ErrBadRequest, Status: status, Message: fmt.Sprintf("the server stopped the sign-in (answered %d)", status)}
		}
		if err := sleepCtx(ctx, interval); err != nil {
			return Account{}, err
		}
	}
}

func (f *LoginFlow) post(ctx context.Context, u string, form url.Values) (int, []byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := f.http.Do(req)
	if err != nil {
		return 0, nil, &Error{Kind: ErrUnreachable, Message: "can't reach the server: " + transportReason(err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, &Error{Kind: ErrUnreachable, Message: "the server's answer broke off: " + err.Error()}
	}
	return resp.StatusCode, data, nil
}
