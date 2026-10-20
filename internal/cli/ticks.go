// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/martinhammer/tickcli/tickbuddy"
)

func (a *App) ticksCmd() *cobra.Command {
	var fromS, toS, yearS string
	cmd := &cobra.Command{
		Use:   "ticks [track] [-d date | --from date [--to date] | --year YYYY]",
		Short: "A track's value on a day or on each day of a range (default: today)",
		Long: `A track's value on a day (default: today), or on each day of a range with
--from and --to or --year. Every day is listed, an unticked one as no or 0.
Without a track, the today view's tracks for each day, as tickcli shows them;
--private and --archived add those.

Piped, each line is the date, the track, yes/no or the count, and the note
if any, separated by tabs.`,
		Example: `  tickcli ticks "Nightly backup" | cut -f3
  tickcli ticks Exercise --year 2025 --json | jq '[.tracks[].days[] | select(.value)] | length'
  tickcli ticks "Cups of coffee" --from -6
  tickcli ticks -d yesterday`,
		Args: args(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			from, to, err := a.ticksRange(fromS, toS, yearS)
			if err != nil {
				return err
			}
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			var tracks []tickbuddy.Track
			named := len(args) == 1
			if named {
				all, err := c.Tracks(ctx, true)
				if err != nil {
					return err
				}
				t, err := tickbuddy.Resolve(all, args[0], a.interactive())
				if err != nil {
					return err
				}
				tracks = []tickbuddy.Track{t}
			} else {
				all, err := c.Tracks(ctx, a.opts.archived)
				if err != nil {
					return err
				}
				tracks = a.listed(all)
			}
			j, err := readJournal(ctx, c, from, to)
			if err != nil {
				return err
			}
			var days []tickbuddy.Date
			for d := from; !to.Before(d); d = d.AddDays(1) {
				days = append(days, d)
			}

			switch a.mode() {
			case jsonMode:
				type day struct {
					Date  tickbuddy.Date  `json:"date"`
					Value tickbuddy.Value `json:"value"`
					Note  *string         `json:"note,omitempty"`
				}
				type row struct {
					Track   string `json:"track"`
					TrackID int    `json:"trackId"`
					Type    string `json:"type"`
					Days    []day  `json:"days"`
				}
				rows := []row{}
				for _, t := range tracks {
					r := row{Track: t.Name, TrackID: t.ID, Type: t.Type}
					for _, d := range days {
						v := j.row(t, d)
						r.Days = append(r.Days, day{d, v.Value, v.Note})
					}
					rows = append(rows, r)
				}
				return a.writeJSON(map[string]any{"from": from, "to": to, "tracks": rows})
			case plain:
				for _, d := range days {
					a.printDay(d, j.rows(tracks, d))
				}
				return nil
			}
			if !named {
				for i, d := range days {
					if i > 0 {
						a.printf("\n")
					}
					a.printDay(d, j.rows(tracks, d))
				}
				return nil
			}
			a.printTrackDays(tracks[0], days, j)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "", "first day of a range (to today, or to --to)")
	cmd.Flags().StringVar(&toS, "to", "", "last day of a range (needs --from)")
	cmd.Flags().StringVar(&yearS, "year", "", "the days 1 Jan to 31 Dec of a year (YYYY), up to today in the current year")
	return cmd
}

// ticksRange reads ticks' -d, --from, --to and --year: today when none is
// given.
func (a *App) ticksRange(fromS, toS, yearS string) (from, to tickbuddy.Date, err error) {
	given := 0
	for _, g := range []bool{a.opts.date != "", fromS != "" || toS != "", yearS != ""} {
		if g {
			given++
		}
	}
	if given > 1 {
		return from, to, usagef("use -d, --from/--to or --year, not more than one")
	}
	if toS != "" && fromS == "" {
		return from, to, usagef("--to needs --from")
	}
	if fromS == "" && yearS == "" {
		d, err := a.day()
		return d, d, err
	}
	today := a.today()
	if from, to, err = period(fromS, toS, yearS, today); err != nil {
		return from, to, err
	}
	if to.IsZero() || (yearS != "" && to.Year() == today.Year()) {
		to = today
	}
	if to.Before(from) {
		return from, to, usagef("--from must be on or before --to")
	}
	return from, to, nil
}

// printTrackDays prints one track's days for a terminal: a heading, then a
// line per day with its value, and the note below.
func (a *App) printTrackDays(t tickbuddy.Track, days []tickbuddy.Date, j journal) {
	from, to := days[0], days[len(days)-1]
	a.printf("%s\n", a.paint(bold, flat(t.Name)+", "+periodText(from, to)))
	label := shortDay
	if from.Year() != to.Year() {
		label = longDay
	}
	dw, w := 0, 1
	for _, d := range days {
		dw = max(dw, width(label(d)))
		w = max(w, width(symbol(j.row(t, d).Value)))
	}
	for _, d := range days {
		r := j.row(t, d)
		sym := symbol(r.Value)
		a.printf("  %s   %s%s\n", pad(label(d), dw), strings.Repeat(" ", w-width(sym)), a.paintSymbol(sym))
		if r.Note != nil {
			for _, l := range noteLines(*r.Note) {
				a.printf("%s%s\n", strings.Repeat(" ", dw+w+6), a.paint(dim, l))
			}
		}
	}
}
