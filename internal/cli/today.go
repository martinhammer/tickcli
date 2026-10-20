// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"strings"

	"github.com/martinhammer/tickcli/tickbuddy"
)

type dayRow struct {
	Track   string          `json:"track"`
	TrackID int             `json:"trackId"`
	Type    string          `json:"type"`
	Value   tickbuddy.Value `json:"value"`
	Note    *string         `json:"note,omitempty"`
}

// trackDay is one track's day.
type trackDay struct {
	track int
	date  tickbuddy.Date
}

// journal is the values and notes of a run of days.
type journal struct {
	values map[trackDay]int
	notes  map[trackDay]string
}

// readJournal reads the ticks from from to to in one request, and their notes
// when the server has notes.
func readJournal(ctx context.Context, c *tickbuddy.Client, from, to tickbuddy.Date) (journal, error) {
	j := journal{values: map[trackDay]int{}, notes: map[trackDay]string{}}
	ticks, err := c.Ticks(ctx, from, to)
	if err != nil {
		return j, err
	}
	for _, t := range ticks {
		j.values[trackDay{t.TrackID, t.Date}] = t.Value
	}
	if c.Has(tickbuddy.FeatureNotes) {
		notes, err := c.Notes(ctx, from, to)
		if err != nil {
			return j, err
		}
		for _, n := range notes {
			j.notes[trackDay{n.TrackID, n.Date}] = n.Text
		}
	}
	return j, nil
}

// row is a track's day, unticked when the journal has no tick.
func (j journal) row(t tickbuddy.Track, day tickbuddy.Date) dayRow {
	k := trackDay{t.ID, day}
	r := dayRow{Track: t.Name, TrackID: t.ID, Type: t.Type, Value: tickbuddy.ValueFor(t, j.values[k])}
	if text, ok := j.notes[k]; ok {
		r.Note = &text
	}
	return r
}

func (j journal) rows(tracks []tickbuddy.Track, day tickbuddy.Date) []dayRow {
	var rows []dayRow
	for _, t := range tracks {
		rows = append(rows, j.row(t, day))
	}
	return rows
}

// runToday shows a day's active tracks with their values and notes, and
// archived ones with --archived.
func (a *App) runToday(ctx context.Context) error {
	day, err := a.day()
	if err != nil {
		return err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return err
	}
	tracks, err := c.Tracks(ctx, a.opts.archived)
	if err != nil {
		return err
	}
	j, err := readJournal(ctx, c, day, day)
	if err != nil {
		return err
	}
	rows := j.rows(a.listed(tracks), day)
	if a.mode() == jsonMode {
		if rows == nil {
			rows = []dayRow{}
		}
		return a.writeJSON(map[string]any{"date": day, "tracks": rows})
	}
	a.printDay(day, rows)
	return nil
}

// printDay prints a day's rows as the today view does, plain or pretty.
func (a *App) printDay(day tickbuddy.Date, rows []dayRow) {
	if a.mode() == plain {
		for _, r := range rows {
			line := []string{day.String(), flat(r.Track), r.Value.String()}
			if r.Note != nil {
				line = append(line, flat(*r.Note))
			}
			a.printf("%s\n", strings.Join(line, "\t"))
		}
		return
	}

	a.printf("%s\n", a.paint(bold, longDay(day)))
	if len(rows) == 0 {
		a.printf("  No tracks yet.\n")
		return
	}
	w := 1
	for _, r := range rows {
		w = max(w, width(symbol(r.Value)))
	}
	for _, r := range rows {
		sym := symbol(r.Value)
		a.printf("  %s%s  %s\n", strings.Repeat(" ", w-width(sym)), a.paintSymbol(sym), flat(r.Track))
		if r.Note != nil {
			for _, l := range noteLines(*r.Note) {
				a.printf("%s%s\n", strings.Repeat(" ", w+6), a.paint(dim, l))
			}
		}
	}
}
