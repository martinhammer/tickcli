// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/martinhammer/tickcli/tickbuddy"
)

// writeResult is the JSON answer of a write (decision 2).
type writeResult struct {
	Track   string           `json:"track"`
	TrackID int              `json:"trackId"`
	Date    tickbuddy.Date   `json:"date"`
	Value   *tickbuddy.Value `json:"value,omitempty"`
	Unit    *string          `json:"unit,omitempty"`
	Label   *string          `json:"label,omitempty"`
	Note    *string          `json:"note,omitempty"`
}

// archivedTrack is the refusal to write to an archived track (decision 3),
// found before sending, from a 200 that differs from what was sent, or from
// the server's 423. It is ErrReadOnly, exit 6 (decision 25).
type archivedTrack struct{ name string }

func (e *archivedTrack) Error() string {
	return fmt.Sprintf("Track %q is archived, so nothing was changed", e.name)
}

func (e *archivedTrack) Is(target error) bool { return target == tickbuddy.ErrReadOnly }

func archivedError(t tickbuddy.Track) error { return &archivedTrack{flat(t.Name)} }

// readOnly reads a write's error: the server's 423 for an archived track
// becomes the same refusal as before sending; a 423 for another reason keeps
// the server's message.
func readOnly(t tickbuddy.Track, err error) error {
	var e *tickbuddy.ReadOnlyError
	if errors.As(err, &e) && e.Reason == tickbuddy.ReasonArchived {
		return archivedError(t)
	}
	return err
}

// target reads the day, connects, and finds the track a write goes to. It
// refuses a write that needs a feature flag the server lacks (needs) before
// anything is sent.
func (a *App) target(ctx context.Context, query string, needs ...string) (*tickbuddy.Client, tickbuddy.Track, tickbuddy.Date, error) {
	day, err := a.day()
	if err != nil {
		return nil, tickbuddy.Track{}, day, err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return nil, tickbuddy.Track{}, day, err
	}
	for _, f := range needs {
		if !c.Has(f) {
			return nil, tickbuddy.Track{}, day, tickbuddy.NeedsNewerServer(f)
		}
	}
	tracks, err := c.Tracks(ctx, true)
	if err != nil {
		return nil, tickbuddy.Track{}, day, err
	}
	t, err := tickbuddy.Resolve(tracks, query, a.interactive())
	if err != nil {
		return nil, tickbuddy.Track{}, day, err
	}
	return c, t, day, nil
}

// amountError explains a number that isn't one, and the likely cause when
// the number came first: the track goes before it.
func amountError(args []string, what, usage string) error {
	if _, err := strconv.Atoi(args[0]); err == nil {
		return usagef("the track comes first: tickcli %s", usage)
	}
	return usagef("%q isn't %s", args[1], what)
}

// trackFirst is target for a command whose value can only be checked once
// the track's type is known. When no track matches and the first argument
// is a number, it says the track comes first, as amountError does.
func (a *App) trackFirst(ctx context.Context, args []string, usage string, needs ...string) (*tickbuddy.Client, tickbuddy.Track, tickbuddy.Date, error) {
	c, t, day, err := a.target(ctx, args[0], needs...)
	if errors.Is(err, tickbuddy.ErrTrackNotFound) && len(args) == 2 && tickbuddy.ValidNumber(args[0]) {
		return nil, t, day, usagef("the track comes first: tickcli %s", usage)
	}
	return c, t, day, err
}

// wrongCommand refuses a command that isn't for t's type, naming the one
// that is; "" when cmd fits (tick and untick for yes/no, add for counters,
// set for every type but yes/no).
func wrongCommand(t tickbuddy.Track, cmd string) error {
	name := flat(t.Name)
	switch {
	case t.IsNumber() && cmd != "set":
		return usagef("%s is a number; use `tickcli set`", name)
	case t.IsChoice() && cmd != "set":
		return usagef("%s is a choice; use `tickcli set`", name)
	case t.IsCounter() && (cmd == "tick" || cmd == "untick"):
		return usagef("%s is a counter; use `tickcli add` or `tickcli set`", name)
	case t.Type == tickbuddy.TypeYesNo && (cmd == "set" || cmd == "add"):
		return usagef("%s is a yes/no track; use `tickcli tick` or `tickcli untick`", name)
	}
	return nil
}

// noteFlags adds the note options of tick, untick, set and add.
func noteFlags(cmd *cobra.Command) {
	cmd.Flags().String("note", "", `set the day's note ("" deletes it)`)
	cmd.Flags().Bool("append", false, "with --note: add to the day's note on a new line instead")
	cmd.Flags().String("append-note", "", "add to the day's note on a new line, unless it already holds the text")
	cmd.Flags().Bool("force-duplicate", false, "append even text the note already holds")
}

// noteEdit reads the note options: nil without any, or a usage error for a
// combination that doesn't go together (decision 1). --append-note "x" and
// --note "x" --append are the same.
func noteEdit(cmd *cobra.Command) (*tickbuddy.NoteEdit, error) {
	f := cmd.Flags()
	hasNote, hasAppendNote := f.Changed("note"), f.Changed("append-note")
	appendFlag, _ := f.GetBool("append")
	force, _ := f.GetBool("force-duplicate")
	switch {
	case hasNote && hasAppendNote:
		return nil, usagef("use --note or --append-note, not both")
	case appendFlag && !hasNote:
		return nil, usagef("--append goes with --note")
	case force && !appendFlag && !hasAppendNote:
		return nil, usagef("--force-duplicate only goes with an append")
	}
	var e *tickbuddy.NoteEdit
	switch {
	case hasAppendNote:
		text, _ := f.GetString("append-note")
		e = tickbuddy.AppendNote(text)
	case hasNote:
		text, _ := f.GetString("note")
		e = &tickbuddy.NoteEdit{Text: text, Append: appendFlag}
	default:
		return nil, nil
	}
	e.AllowDuplicate = force
	return e, nil
}

// noteNeeds lists the feature flags a NoteEdit needs.
func noteNeeds(e *tickbuddy.NoteEdit) []string {
	switch {
	case e == nil:
		return nil
	case e.Append:
		return []string{tickbuddy.FeatureNotes, tickbuddy.FeatureNotesAppend}
	}
	return []string{tickbuddy.FeatureNotes}
}

// appendRecipe is the help's example of appending, safe to run again.
const appendRecipe = `  # Log each activity's link on the day; running it again for the same
  # activity changes nothing.
  tickcli tick Exercise --append-note "https://www.strava.com/activities/…"`

func (a *App) tickCmd(ticked bool) *cobra.Command {
	name, short, example := "tick", "Tick a yes/no track", appendRecipe
	if !ticked {
		name, short, example = "untick", "Untick a yes/no track", ""
	}
	cmd := &cobra.Command{
		Use:     name + ` <track> [-d date] [--note text [--append [--force-duplicate]] | --append-note text [--force-duplicate]]`,
		Short:   short,
		Example: example,
		Args:    args(1, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			note, err := noteEdit(cmd)
			if err != nil {
				return err
			}
			c, t, day, err := a.target(cmd.Context(), args[0], noteNeeds(note)...)
			if err != nil {
				return err
			}
			if !t.Known() {
				return tickbuddy.NeedsNewerClient(t)
			}
			if err := wrongCommand(t, name); err != nil {
				return err
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			r, err := c.SetYesNo(cmd.Context(), t.ID, day, ticked, note)
			if err != nil {
				return readOnly(t, err)
			}
			if r.Archived {
				return archivedError(t)
			}
			return a.wrote(t, day, r, note)
		},
	}
	noteFlags(cmd)
	return cmd
}

func (a *App) setCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set <track> <value> [-d date] [--note text [--append [--force-duplicate]] | --append-note text [--force-duplicate]]",
		Short: "Set a counter, number or choice on a day",
		Long: `Set a track's value on a day (default: today):

  counter: a whole number; 0 removes the count
  number:  a number with a point and no grouping, such as 10.4 or -1.5;
           the server rounds it to the track's decimal places
  choice:  an option's label (any case), or '#2' for the second option

"" clears a number or choice day. In a script, an empty variable clears the
day too: tickcli set Run "$km" with $km empty leaves Run without a value.
Quote '#2', since # starts a comment in a shell.`,
		Example: `  tickcli set "Cups of coffee" 3
  tickcli set Run 10.4
  tickcli set "Greenhouse low" -1.5
  tickcli set "Greenhouse low" "$(read-sensor)"
  tickcli set "Mum's mood" happy --note "Calmer after lunch"
  tickcli set "Mum's mood" '#2'
  tickcli set Run ""`,
		Args: args(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			note, err := noteEdit(cmd)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			c, t, day, err := a.trackFirst(ctx, args, "set <track> <value>", noteNeeds(note)...)
			if err != nil {
				return err
			}
			if !t.Known() {
				return tickbuddy.NeedsNewerClient(t)
			}
			if err := wrongCommand(t, "set"); err != nil {
				return err
			}
			value := strings.TrimSpace(args[1])
			var send func() (tickbuddy.Result, error)
			switch {
			case t.IsCounter():
				n, err := strconv.Atoi(args[1])
				if err != nil || n < 0 {
					return usagef("%q isn't a count; use a whole number, 0 or more", args[1])
				}
				send = func() (tickbuddy.Result, error) { return c.SetCount(ctx, t.ID, day, n, note) }
			case value == "":
				send = func() (tickbuddy.Result, error) { return c.ClearValue(ctx, t.ID, day, note) }
			case t.IsNumber():
				if !tickbuddy.ValidNumber(value) {
					return usagef(`%s takes a number with a point, such as 10.4, or "" to clear the day`, flat(t.Name))
				}
				send = func() (tickbuddy.Result, error) { return c.SetNumber(ctx, t.ID, day, value, note) }
			default:
				o, err := tickbuddy.ResolveOption(t, value, a.interactive())
				if err != nil {
					return optionError(err)
				}
				send = func() (tickbuddy.Result, error) { return c.SetChoice(ctx, t.ID, day, o.Value, note) }
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			r, err := send()
			if err != nil {
				return readOnly(t, err)
			}
			if r.Archived {
				return archivedError(t)
			}
			return a.wrote(t, day, r, note)
		},
	}
	noteFlags(cmd)
	return cmd
}

// optionError is ResolveOption's error with the track's name and the labels
// cleaned for a terminal, one line each.
func optionError(err error) error {
	var oe *tickbuddy.OptionError
	if !errors.As(err, &oe) {
		return err
	}
	out := *oe
	out.Track, out.Candidates = flat(oe.Track), nil
	for _, o := range oe.Candidates {
		out.Candidates = append(out.Candidates, tickbuddy.ChoiceOption{Value: o.Value, Label: flat(o.Label)})
	}
	return &out
}

func (a *App) addCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <track> [n] [-d date] [--note text [--append [--force-duplicate]] | --append-note text [--force-duplicate]]",
		Short: "Add n (default 1) to a counter; negative takes away",
		Long: `Add n (default 1) to a counter; a negative n takes away, never below 0.
add is for counters only. To add to a number, read the day with tickcli
ticks and set the new total with tickcli set.`,
		Args: args(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := 1
			var amountErr error
			if len(args) == 2 {
				var err error
				n, err = strconv.Atoi(args[1])
				if err != nil || n == 0 {
					amountErr = amountError(args, "an amount to add; use a whole number other than 0", "add <track> [n]")
				}
			}
			if _, err := strconv.Atoi(args[0]); err == nil && amountErr != nil {
				return amountErr
			}
			note, err := noteEdit(cmd)
			if err != nil {
				return err
			}
			c, t, day, err := a.target(cmd.Context(), args[0], noteNeeds(note)...)
			if err != nil {
				return err
			}
			if !t.Known() {
				return tickbuddy.NeedsNewerClient(t)
			}
			// The track's type first: a Number's "add Run 2.5" is told to
			// use set, not that 2.5 isn't a whole number.
			if err := wrongCommand(t, "add"); err != nil {
				return err
			}
			if amountErr != nil {
				return amountErr
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			r, err := c.Add(cmd.Context(), t.ID, day, n, note)
			if errors.Is(err, tickbuddy.ErrUnreachable) {
				return fmt.Errorf("%w. The server may or may not have counted this; check with `tickcli` before adding again", err)
			}
			if err != nil {
				return readOnly(t, err)
			}
			if r.Archived {
				return archivedError(t)
			}
			return a.wrote(t, day, r, note)
		},
	}
	noteFlags(cmd)
	return cmd
}

// wrote reports a write: one line in a terminal, nothing when piped.
func (a *App) wrote(t tickbuddy.Track, day tickbuddy.Date, r tickbuddy.Result, sent *tickbuddy.NoteEdit) error {
	switch a.mode() {
	case jsonMode:
		out := writeResult{Track: t.Name, TrackID: t.ID, Date: day, Value: &r.Value}
		out.Unit, out.Label = valueExtras(t, r.Value)
		if sent != nil {
			out.Note = r.Note
		}
		return a.writeJSON(out)
	case plain:
		return nil
	}
	var line string
	switch {
	case t.IsCounter():
		line = fmt.Sprintf("%s: %d · %s", flat(t.Name), r.Value.Int(), shortDay(day))
	case (t.IsNumber() || t.IsChoice()) && r.Value.IsNull():
		line = fmt.Sprintf("%s: no value · %s", flat(t.Name), shortDay(day))
	case t.IsNumber() || t.IsChoice():
		line = fmt.Sprintf("%s: %s · %s", flat(t.Name), a.symbol(t, r.Value), shortDay(day))
	default:
		line = fmt.Sprintf("%s %s · %s", a.paintSymbol(a.symbol(t, r.Value)), flat(t.Name), shortDay(day))
	}
	if sent != nil {
		line += "  " + noteText(r.Note, sent.Append)
	}
	a.printf("%s\n", line)
	return nil
}

// noteText shows the note after a write, its further lines indented below;
// appended says whether the write appended rather than replaced.
func noteText(note *string, appended bool) string {
	if note == nil || *note == "" {
		if appended {
			return "📝 no note"
		}
		return "📝 note removed"
	}
	return "📝 " + strings.Join(noteLines(*note), "\n    ")
}

func (a *App) noteCmd() *cobra.Command {
	var clear, appendFlag, force bool
	cmd := &cobra.Command{
		Use:   "note <track> (<text> [--append [--force-duplicate]] | --clear) [-d date]",
		Short: "Set, replace, append to (--append) or delete (--clear) a day's note",
		Example: `  tickcli note Exercise "Ran 5 km"
  tickcli note Exercise "https://www.strava.com/activities/…" --append`,
		Args: args(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case appendFlag && clear:
				return usagef("use --append or --clear, not both")
			case clear == (len(args) == 2):
				return usagef("give the note's text, or --clear to delete it")
			case force && !appendFlag:
				return usagef("--force-duplicate only goes with an append")
			}
			text := ""
			if !clear {
				text = args[1]
			}
			needs := []string{tickbuddy.FeatureNotes}
			if appendFlag {
				needs = append(needs, tickbuddy.FeatureNotesAppend)
			}
			c, t, day, err := a.target(cmd.Context(), args[0], needs...)
			if err != nil {
				return err
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			// SetNote rather than DeleteNote for --clear: its answer shows
			// whether the track was archived meanwhile.
			var stored string
			if appendFlag {
				stored, err = c.AppendNote(cmd.Context(), t.ID, day, text, force)
			} else {
				stored, err = c.SetNote(cmd.Context(), t.ID, day, text)
			}
			if err != nil {
				return readOnly(t, err)
			}
			if appendFlag && !tickbuddy.NoteContains(stored, text) || !appendFlag && stored != tickbuddy.NormalizeNote(text) {
				return archivedError(t)
			}
			switch a.mode() {
			case jsonMode:
				return a.writeJSON(writeResult{Track: t.Name, TrackID: t.ID, Date: day, Note: &stored})
			case pretty:
				a.printf("%s · %s  %s\n", flat(t.Name), shortDay(day), noteText(&stored, appendFlag))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&clear, "clear", false, "delete the note")
	cmd.Flags().BoolVar(&appendFlag, "append", false, "add the text to the note on a new line, unless it already holds it")
	cmd.Flags().BoolVar(&force, "force-duplicate", false, "with --append: append even text the note already holds")
	return cmd
}
