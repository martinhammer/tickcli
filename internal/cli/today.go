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

// runToday shows a day's active tracks with their values and notes.
func (a *App) runToday(ctx context.Context) error {
	day, err := a.day()
	if err != nil {
		return err
	}
	c, err := a.connect(ctx)
	if err != nil {
		return err
	}
	tracks, err := c.Tracks(ctx, false)
	if err != nil {
		return err
	}
	ticks, err := c.Ticks(ctx, day, day)
	if err != nil {
		return err
	}
	notes := map[int]string{}
	if c.Has(tickbuddy.FeatureNotes) {
		ns, err := c.Notes(ctx, day, day)
		if err != nil {
			return err
		}
		for _, n := range ns {
			notes[n.TrackID] = n.Text
		}
	}
	values := map[int]int{}
	for _, t := range ticks {
		if t.Date == day {
			values[t.TrackID] = t.Value
		}
	}

	var rows []dayRow
	for _, t := range a.visible(tracks) {
		r := dayRow{Track: t.Name, TrackID: t.ID, Type: t.Type, Value: tickbuddy.ValueFor(t, values[t.ID])}
		if text, ok := notes[t.ID]; ok {
			r.Note = &text
		}
		rows = append(rows, r)
	}

	switch a.mode() {
	case jsonMode:
		if rows == nil {
			rows = []dayRow{}
		}
		return a.writeJSON(map[string]any{"date": day, "tracks": rows})
	case plain:
		for _, r := range rows {
			line := []string{day.String(), flat(r.Track), r.Value.String()}
			if r.Note != nil {
				line = append(line, flat(*r.Note))
			}
			a.printf("%s\n", strings.Join(line, "\t"))
		}
		return nil
	}

	a.printf("%s\n", a.paint(bold, longDay(day)))
	if len(rows) == 0 {
		a.printf("  No tracks yet.\n")
		return nil
	}
	w := 1
	for _, r := range rows {
		w = max(w, width(symbol(r.Value)))
	}
	for _, r := range rows {
		sym := symbol(r.Value)
		a.printf("  %s%s  %s\n", strings.Repeat(" ", w-width(sym)), a.paintSymbol(sym), flat(r.Track))
		if r.Note != nil {
			a.printf("%s%s\n", strings.Repeat(" ", w+6), a.paint(dim, firstLine(*r.Note)))
		}
	}
	return nil
}
