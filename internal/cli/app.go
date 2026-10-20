// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli is the tickcli command: its commands, output and exit codes.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/martinhammer/tickcli/tickbuddy"
)

// version is set at build time with -ldflags "-X …/internal/cli.version=…".
var version = "dev"

// Exit codes (D12). The README lists them.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitAuth        = 3
	exitNotFound    = 4
	exitUnreachable = 5
	exitReadOnly    = 6
)

// App is one run of tickcli. Its fields are the outside world, replaceable
// in tests.
type App struct {
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
	StdinTTY  bool
	StdoutTTY bool
	Getenv    func(string) string
	Now       func() time.Time
	Store     *tickbuddy.Store
	Hostname  string
	// ReadPassword reads a line from the terminal without echo.
	ReadPassword func() ([]byte, error)
	// PollInterval is how often login polls the server.
	PollInterval time.Duration

	opts  options
	creds *tickbuddy.StoredCredentials
}

type options struct {
	json     bool
	private  bool
	archived bool
	date     string
	retries  int
}

// Main runs tickcli on the real terminal and returns the exit code.
func Main() int {
	host, _ := os.Hostname()
	a := &App{
		Stdin:        os.Stdin,
		Stdout:       os.Stdout,
		Stderr:       os.Stderr,
		StdinTTY:     term.IsTerminal(int(os.Stdin.Fd())),
		StdoutTTY:    term.IsTerminal(int(os.Stdout.Fd())),
		Getenv:       os.Getenv,
		Now:          time.Now,
		Store:        tickbuddy.DefaultStore(),
		Hostname:     host,
		ReadPassword: func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) },
		PollInterval: tickbuddy.LoginPollInterval,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return a.Run(ctx, os.Args[1:])
}

// Run runs tickcli with args and returns the exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	a.opts = options{retries: 3}
	a.creds = nil
	root := a.rootCmd()
	root.SetArgs(protectNegatives(args))
	root.SetIn(a.Stdin)
	root.SetOut(a.Stdout)
	root.SetErr(a.Stderr)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	return a.fail(err)
}

// usageError is bad usage: exit 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{fmt.Sprintf(format, args...)}
}

// codedError is an error with its own exit code.
type codedError struct {
	err  error
	code int
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

func exitCode(err error) int {
	var ue *usageError
	var ce *codedError
	switch {
	case errors.As(err, &ce):
		return ce.code
	case errors.As(err, &ue), errors.Is(err, tickbuddy.ErrBadDate):
		return exitUsage
	case errors.Is(err, tickbuddy.ErrNoCredentials), errors.Is(err, tickbuddy.ErrUnauthorized):
		return exitAuth
	case errors.Is(err, tickbuddy.ErrTrackNotFound), errors.Is(err, tickbuddy.ErrAmbiguous):
		return exitNotFound
	case errors.Is(err, tickbuddy.ErrUnreachable):
		return exitUnreachable
	case errors.Is(err, tickbuddy.ErrReadOnly):
		return exitReadOnly
	}
	return exitError
}

// message is how an error reads to a person.
func message(err error) string {
	switch {
	case errors.Is(err, tickbuddy.ErrUnauthorized):
		return "the server refused the stored credentials; sign in again with `tickcli login <url>`"
	case errors.Is(err, tickbuddy.ErrNoCredentials) && err == tickbuddy.ErrNoCredentials:
		return "not signed in; run `tickcli login <url>`, or set TICKBUDDY_URL, TICKBUDDY_USER and TICKBUDDY_APP_PASSWORD"
	case errors.Is(err, context.Canceled):
		return "interrupted"
	}
	return clean(err.Error())
}

func (a *App) fail(err error) int {
	code := exitCode(err)
	msg := message(err)
	if errors.Is(err, tickbuddy.ErrUnauthorized) && a.creds != nil {
		if _, src, _ := a.creds.Account(context.Background()); src.Kind == tickbuddy.SourceEnvironment {
			msg = "the server refused the user and app password in the TICKBUDDY_* environment variables"
		}
	}
	if a.opts.json {
		enc := newJSONEncoder(a.Stderr)
		_ = enc.Encode(map[string]any{"error": msg, "code": code})
	} else {
		fmt.Fprintf(a.Stderr, "tickcli: %s\n", msg)
	}
	return code
}

// protectNegatives lets `tickcli add coffee -2` mean "take 2 away". When a
// bare negative number is among the arguments, the flags (with their values)
// and the command name go first, then "--", then the other arguments in
// their original order, so the flag parser leaves the number alone and the
// track still has to come before it.
func protectNegatives(args []string) []string {
	if slices.Contains(args, "--") {
		return args
	}
	negative := regexp.MustCompile(`^-[0-9]+$`)
	takesValue := map[string]bool{"-d": true, "--date": true, "--note": true, "--append-note": true, "--retries": true, "--from": true, "--to": true}
	var flags, positional []string
	found := false
	for i := 0; i < len(args); i++ {
		switch {
		case takesValue[args[i]] && i+1 < len(args):
			flags = append(flags, args[i], args[i+1])
			i++
		case negative.MatchString(args[i]):
			found = true
			positional = append(positional, args[i])
		case strings.HasPrefix(args[i], "-") && args[i] != "-":
			flags = append(flags, args[i])
		default:
			positional = append(positional, args[i])
		}
	}
	if !found || len(positional) == 0 || negative.MatchString(positional[0]) {
		return args
	}
	out := append(flags, positional[0], "--")
	return append(out, positional[1:]...)
}

// today is the machine's today.
func (a *App) today() tickbuddy.Date { return tickbuddy.DateOf(a.Now()) }

// day is --date, or today.
func (a *App) day() (tickbuddy.Date, error) {
	if a.opts.date == "" {
		return a.today(), nil
	}
	return tickbuddy.ParseDate(a.opts.date, a.today())
}

// interactive: a person at a terminal, so track name prefixes match.
func (a *App) interactive() bool { return a.StdinTTY && a.StdoutTTY }

func (a *App) userAgent() string { return "Tickcli/" + version }

// client returns a client for the stored (or environment) credentials.
func (a *App) client() *tickbuddy.Client {
	return tickbuddy.New(a.credentials(), a.clientOptions()...)
}

// credentials are looked up once per run; fail reads them too.
func (a *App) credentials() *tickbuddy.StoredCredentials {
	if a.creds == nil {
		a.creds = tickbuddy.NewStoredCredentials(a.Store)
	}
	return a.creds
}

func (a *App) clientOptions() []tickbuddy.Option {
	return []tickbuddy.Option{tickbuddy.WithUserAgent(a.userAgent()), tickbuddy.WithRetries(a.opts.retries)}
}

// connect returns a client whose capabilities are read, so Has works.
func (a *App) connect(ctx context.Context) (*tickbuddy.Client, error) {
	c := a.client()
	if _, err := c.Capabilities(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// visible keeps the tracks lists show: private ones only with --private.
func (a *App) visible(tracks []tickbuddy.Track) []tickbuddy.Track {
	var out []tickbuddy.Track
	for _, t := range tracks {
		if t.Private && !a.opts.private {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (a *App) rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "tickcli",
		Short: "Tick Tickbuddy tracks from the command line",
		Long: `Tickcli is the command-line client for Tickbuddy, the habit tracker for Nextcloud.
Run without a command, it shows today's tracks.

Exit codes: 0 done, 1 error, 2 bad usage, 3 not signed in or credentials
refused, 4 track not found or ambiguous, 5 server or network unreachable,
6 track is read-only (archived), so nothing was changed.`,
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q; see `tickcli --help`", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error { return a.runToday(cmd.Context()) },
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error { return &usageError{err.Error()} })
	root.SetVersionTemplate("tickcli {{.Version}}\n")

	pf := root.PersistentFlags()
	pf.BoolVar(&a.opts.json, "json", false, "write JSON")
	pf.BoolVar(&a.opts.private, "private", false, "include private tracks in lists")
	pf.BoolVar(&a.opts.archived, "archived", false, "include archived tracks in lists")
	pf.StringVarP(&a.opts.date, "date", "d", "", "the day: YYYY-MM-DD, today, yesterday or -N (N days ago)")
	pf.IntVar(&a.opts.retries, "retries", 3, "how often to retry a safe request when the server can't be reached")

	root.AddCommand(
		a.loginCmd(), a.logoutCmd(), a.statusCmd(),
		a.tickCmd(true), a.tickCmd(false), a.setCmd(), a.addCmd(),
		a.noteCmd(), a.notesCmd(), a.ticksCmd(),
		a.weekCmd(), a.statsCmd(), a.tracksCmd(), a.exportCmd(),
	)
	return root
}

// args checks the number of arguments, as bad usage.
func args(min, max int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) < min || len(args) > max {
			return usagef("wrong number of arguments; usage: tickcli %s", cmd.Use)
		}
		return nil
	}
}
