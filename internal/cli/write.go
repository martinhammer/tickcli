// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/martinhammer/tickcli/tickbuddy"
)

// writeResult is the JSON answer of a write (decision 2).
type writeResult struct {
	Track   string           `json:"track"`
	TrackID int              `json:"trackId"`
	Date    tickbuddy.Date   `json:"date"`
	Value   *tickbuddy.Value `json:"value,omitempty"`
	Note    *string          `json:"note,omitempty"`
}

// archivedTrack is the refusal to write to an archived track (decision 3).
type archivedTrack struct{ name string }

func (e *archivedTrack) Error() string {
	return fmt.Sprintf("Track %q is archived, so nothing was changed", e.name)
}

func archivedError(t tickbuddy.Track) error { return &archivedTrack{flat(t.Name)} }

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
			if t.IsCounter() {
				return usagef("%s is a counter; use `tickcli add` or `tickcli set`", flat(t.Name))
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			r, err := c.SetYesNo(cmd.Context(), t.ID, day, ticked, note)
			if err != nil {
				return err
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
		Use:   "set <track> <n> [-d date] [--note text [--append [--force-duplicate]] | --append-note text [--force-duplicate]]",
		Short: "Set a counter to n (0 removes the count)",
		Args:  args(2, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 0 {
				return amountError(args, "a count; use a whole number, 0 or more", "set <track> <n>")
			}
			note, err := noteEdit(cmd)
			if err != nil {
				return err
			}
			c, t, day, err := a.target(cmd.Context(), args[0], noteNeeds(note)...)
			if err != nil {
				return err
			}
			if !t.IsCounter() {
				return usagef("%s is a yes/no track; use `tickcli tick` or `tickcli untick`", flat(t.Name))
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			r, err := c.SetCount(cmd.Context(), t.ID, day, n, note)
			if err != nil {
				return err
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

func (a *App) addCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <track> [n] [-d date] [--note text [--append [--force-duplicate]] | --append-note text [--force-duplicate]]",
		Short: "Add n (default 1) to a counter; negative takes away",
		Args:  args(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := 1
			if len(args) == 2 {
				var err error
				n, err = strconv.Atoi(args[1])
				if err != nil || n == 0 {
					return amountError(args, "an amount to add; use a whole number other than 0", "add <track> [n]")
				}
			}
			note, err := noteEdit(cmd)
			if err != nil {
				return err
			}
			c, t, day, err := a.target(cmd.Context(), args[0], noteNeeds(note)...)
			if err != nil {
				return err
			}
			if !t.IsCounter() {
				return usagef("%s is a yes/no track; use `tickcli tick` or `tickcli untick`", flat(t.Name))
			}
			if t.IsArchived() {
				return archivedError(t)
			}
			r, err := c.Add(cmd.Context(), t.ID, day, n, note)
			if errors.Is(err, tickbuddy.ErrUnreachable) {
				return fmt.Errorf("%w. The server may or may not have counted this; check with `tickcli` before adding again", err)
			}
			if err != nil {
				return err
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
		if sent != nil {
			out.Note = r.Note
		}
		return a.writeJSON(out)
	case plain:
		return nil
	}
	var line string
	if t.IsCounter() {
		line = fmt.Sprintf("%s: %d · %s", flat(t.Name), r.Value.Int(), shortDay(day))
	} else {
		line = fmt.Sprintf("%s %s · %s", a.paintSymbol(symbol(r.Value)), flat(t.Name), shortDay(day))
	}
	if sent != nil {
		line += "  " + noteText(r.Note, sent.Append)
	}
	a.printf("%s\n", line)
	return nil
}

// noteText shows the note after a write; appended says whether the write
// appended rather than replaced.
func noteText(note *string, appended bool) string {
	if note == nil || *note == "" {
		if appended {
			return "📝 no note"
		}
		return "📝 note removed"
	}
	return "📝 " + firstLine(*note)
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
				return err
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
