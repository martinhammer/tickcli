// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"

	"github.com/martinhammer/tickcli/tickbuddy"
)

func (a *App) loginCmd() *cobra.Command {
	var paste, qr, insecure bool
	cmd := &cobra.Command{
		Use:   "login <url> [--paste] [--qr] [--insecure-http]",
		Short: "Sign in to a Nextcloud with Tickbuddy",
		Long: `Sign in to a Nextcloud with Tickbuddy.

By default this prints a link (and with --qr a QR code of it) to open in a
browser, where you grant access; Nextcloud then creates an app password named
"Tickcli on <this host>". With --paste you type a user and an app password you
created yourself; when standard input isn't a terminal they are read as two
lines from it.`,
		Args: args(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			url, err := tickbuddy.NormalizeURL(args[0], insecure)
			if err != nil {
				return &usageError{err.Error()}
			}
			var acct tickbuddy.Account
			if paste {
				acct, err = a.pasteLogin(url)
			} else {
				acct, err = a.linkLogin(ctx, url, qr)
			}
			if err != nil {
				return err
			}

			c := tickbuddy.New(acct, tickbuddy.WithUserAgent(a.userAgent()), tickbuddy.WithRetries(a.opts.retries))
			caps, err := c.Capabilities(ctx)
			if err != nil {
				if acct.CreatedByLink && !errors.Is(err, tickbuddy.ErrUnauthorized) {
					// Don't leave an app password behind that nothing uses.
					_ = c.RevokeAppPassword(ctx)
				}
				if errors.Is(err, tickbuddy.ErrUnauthorized) {
					return &codedError{errors.New("the server refused this user and app password; nothing was stored"), exitAuth}
				}
				return fmt.Errorf("%w; nothing was stored", err)
			}
			src, err := a.Store.Save(ctx, acct)
			if err != nil {
				if acct.CreatedByLink {
					_ = c.RevokeAppPassword(ctx)
				}
				return fmt.Errorf("couldn't store the credentials: %w", err)
			}

			if a.mode() == jsonMode {
				return a.writeJSON(map[string]string{"user": acct.User, "url": acct.URL, "tickbuddy": caps.Version, "credentials": src.String()})
			}
			a.printf("Signed in as %s · Tickbuddy %s\n", clean(acct.User), clean(caps.Version))
			where := "the keychain"
			if src.Kind == tickbuddy.SourceFile {
				where = fmt.Sprintf("%s, encrypted with %s", a.Store.Path(), strings.TrimSuffix(strings.TrimPrefix(src.String(), "file ("), ")"))
			}
			fmt.Fprintf(a.Stderr, "Credentials stored in %s.\n", where)
			if _, ok, _ := a.Store.FromEnvironment(); ok {
				fmt.Fprintf(a.Stderr, "Note: the TICKBUDDY_* environment variables are set, and they come first.\n")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&paste, "paste", false, "type a user and app password instead of opening a link")
	cmd.Flags().BoolVar(&qr, "qr", false, "also show the link as a QR code, to sign in from a phone")
	cmd.Flags().BoolVar(&insecure, "insecure-http", false, "allow plain http to a public address")
	return cmd
}

func (a *App) linkLogin(ctx context.Context, url string, qr bool) (tickbuddy.Account, error) {
	host := a.Hostname
	if host == "" {
		host = "this computer"
	}
	flow, err := tickbuddy.StartLogin(ctx, url, "Tickcli on "+host, nil)
	if err != nil {
		return tickbuddy.Account{}, err
	}
	fmt.Fprintf(a.Stderr, "Open this link in a browser, sign in and grant access:\n\n  %s\n\n", clean(flow.LoginURL))
	if qr {
		qrterminal.GenerateWithConfig(flow.LoginURL, qrterminal.Config{
			Level:          qrterminal.L,
			Writer:         a.Stderr,
			HalfBlocks:     true,
			BlackChar:      qrterminal.BLACK_BLACK,
			WhiteBlackChar: qrterminal.WHITE_BLACK,
			WhiteChar:      qrterminal.WHITE_WHITE,
			BlackWhiteChar: qrterminal.BLACK_WHITE,
			QuietZone:      2,
		})
		fmt.Fprintln(a.Stderr)
	}
	fmt.Fprintf(a.Stderr, "Waiting for you to grant access (Ctrl-C to stop)…\n")
	wctx, cancel := context.WithTimeout(ctx, tickbuddy.LoginTimeout)
	defer cancel()
	acct, err := flow.Wait(wctx, a.PollInterval)
	if errors.Is(err, context.DeadlineExceeded) {
		return tickbuddy.Account{}, errors.New("nobody granted access within 20 minutes, and the server has forgotten the sign-in; run `tickcli login` again")
	}
	return acct, err
}

func (a *App) pasteLogin(url string) (tickbuddy.Account, error) {
	in := bufio.NewReader(a.Stdin)
	readLine := func() (string, error) {
		s, err := in.ReadString('\n')
		if err != nil && s == "" {
			return "", err
		}
		return strings.TrimRight(s, "\r\n"), nil
	}
	var user, password string
	var err error
	if a.StdinTTY {
		fmt.Fprintf(a.Stderr, "User: ")
		if user, err = readLine(); err != nil {
			return tickbuddy.Account{}, err
		}
		fmt.Fprintf(a.Stderr, "App password: ")
		pw, err := a.ReadPassword()
		fmt.Fprintln(a.Stderr)
		if err != nil {
			return tickbuddy.Account{}, err
		}
		password = string(pw)
	} else {
		user, err = readLine()
		if err == nil {
			password, err = readLine()
		}
		if err != nil {
			return tickbuddy.Account{}, usagef("expected the user and the app password as two lines on standard input")
		}
	}
	user, password = strings.TrimSpace(user), strings.TrimSpace(password)
	if user == "" || password == "" {
		return tickbuddy.Account{}, usagef("both a user and an app password are needed")
	}
	return tickbuddy.Account{URL: url, User: user, AppPassword: password}, nil
}

func (a *App) logoutCmd() *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "logout [--local]",
		Short: "Forget the stored credentials, revoking an app password Tickcli created",
		Args:  args(0, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if _, ok, _ := a.Store.FromEnvironment(); ok {
				a.say("The TICKBUDDY_* environment variables are set, so there is nothing stored to forget.")
				return nil
			}
			acct, _, err := a.Store.LoadStored(ctx)
			if errors.Is(err, tickbuddy.ErrNoCredentials) {
				a.say("Not signed in; there is nothing to forget.")
				return nil
			}
			revoked := false
			if err == nil && acct.CreatedByLink && !local {
				c := tickbuddy.New(acct, tickbuddy.WithUserAgent(a.userAgent()), tickbuddy.WithRetries(a.opts.retries))
				switch err := c.RevokeAppPassword(ctx); {
				case err == nil:
					revoked = true
				case errors.Is(err, tickbuddy.ErrUnauthorized):
					// already revoked on the server
				default:
					return fmt.Errorf("couldn't revoke the app password on the server, so nothing was forgotten: %w (run `tickcli logout --local` to only forget it, then remove it under Settings → Security → Devices & sessions)", err)
				}
			}
			if _, err := a.Store.Delete(ctx); err != nil {
				return fmt.Errorf("couldn't forget the credentials: %w", err)
			}
			switch {
			case revoked:
				a.say("Signed out; the app password was revoked on the server.")
			case err == nil && !acct.CreatedByLink:
				a.say("Signed out. The app password you pasted still works on the server; remove it under Settings → Security → Devices & sessions if nothing else uses it.")
			default:
				a.say("Signed out.")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "only forget the credentials; don't revoke the app password")
	return cmd
}

// say prints a message for a person: stdout in a terminal, stderr when
// piped or with --json, so scripts see only data.
func (a *App) say(msg string) {
	if a.mode() == pretty {
		a.printf("%s\n", msg)
		return
	}
	fmt.Fprintln(a.Stderr, msg)
}

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show who is signed in, where, and what the server supports",
		Args:  args(0, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c := a.client()
			acct, src, err := a.creds.Account(ctx)
			if err != nil {
				return err
			}
			caps, err := c.Capabilities(ctx)
			if err != nil {
				return err
			}
			var missing []string
			for _, f := range tickbuddy.Features {
				if !caps.Features[f] {
					missing = append(missing, f)
				}
			}
			switch a.mode() {
			case jsonMode:
				if missing == nil {
					missing = []string{}
				}
				return a.writeJSON(map[string]any{"user": acct.User, "url": acct.URL, "tickbuddy": caps.Version, "credentials": src.String(), "missingFeatures": missing})
			case plain:
				a.printf("user\t%s\nurl\t%s\ntickbuddy\t%s\ncredentials\t%s\nmissingFeatures\t%s\n", flat(acct.User), flat(acct.URL), flat(caps.Version), src, strings.Join(missing, ","))
				return nil
			}
			a.printf("Signed in as %s at %s · Tickbuddy %s · credentials in %s\n", clean(acct.User), clean(acct.URL), clean(caps.Version), src)
			if len(missing) > 0 {
				a.printf("This server lacks %s; %s.\n", strings.Join(missing, ", "), missingHint(missing))
			}
			return nil
		},
	}
}

func missingHint(missing []string) string {
	var needs []string
	for _, f := range missing {
		switch f {
		case tickbuddy.FeatureStats:
			needs = append(needs, "`stats` and `week` need a newer Tickbuddy")
		case tickbuddy.FeatureNotes:
			needs = append(needs, "notes need a newer Tickbuddy")
		case tickbuddy.FeatureNotesAppend:
			needs = append(needs, "appending to notes needs a newer Tickbuddy")
		case tickbuddy.FeatureBooleanSet, tickbuddy.FeatureCounterIncrement:
			if !slices.Contains(needs, "writes use slower fallbacks") {
				needs = append(needs, "writes use slower fallbacks")
			}
		case tickbuddy.FeatureArchivedTracks:
			needs = append(needs, "archived tracks aren't listed")
		}
	}
	return strings.Join(needs, ", ")
}
