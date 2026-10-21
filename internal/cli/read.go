// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/martinhammer/tickcli/tickbuddy"
)

// listed keeps the tracks a list shows: private ones with --private,
// archived ones with --archived.
func (a *App) listed(tracks []tickbuddy.Track) []tickbuddy.Track {
	var out []tickbuddy.Track
	for _, t := range a.visible(tracks) {
		if t.IsArchived() && !a.opts.archived {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (a *App) notesCmd() *cobra.Command {
	var fromS, toS string
	cmd := &cobra.Command{
		Use:   "notes [--from date] [--to date]",
		Short: "List notes (default: the last 7 days)",
		Args:  args(0, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			today := a.today()
			to, from := today, today.AddDays(-6)
			var err error
			if toS != "" {
				if to, err = tickbuddy.ParseDate(toS, today); err != nil {
					return err
				}
				if fromS == "" {
					from = to.AddDays(-6)
				}
			}
			if fromS != "" {
				if from, err = tickbuddy.ParseDate(fromS, today); err != nil {
					return err
				}
			}
			if to.Before(from) {
				return usagef("--from %s is after --to %s", from, to)
			}
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			notes, err := c.Notes(ctx, from, to)
			if err != nil {
				return err
			}
			tracks, err := c.Tracks(ctx, true)
			if err != nil {
				return err
			}
			byID := map[int]tickbuddy.Track{}
			for _, t := range a.listed(tracks) {
				byID[t.ID] = t
			}
			type row struct {
				Date    tickbuddy.Date `json:"date"`
				Track   string         `json:"track"`
				TrackID int            `json:"trackId"`
				Text    string         `json:"text"`
			}
			rows := []row{}
			for _, n := range notes {
				if t, ok := byID[n.TrackID]; ok {
					rows = append(rows, row{n.Date, t.Name, t.ID, n.Text})
				}
			}
			slices.SortStableFunc(rows, func(x, y row) int { return y.Date.Compare(x.Date) })

			switch a.mode() {
			case jsonMode:
				return a.writeJSON(rows)
			case plain:
				for _, r := range rows {
					a.printf("%s\t%s\t%s\n", r.Date, flat(r.Track), flat(r.Text))
				}
				return nil
			}
			if len(rows) == 0 {
				a.printf("No notes from %s to %s.\n", dayMonthYear(from), dayMonthYear(to))
				return nil
			}
			var last tickbuddy.Date
			for i, r := range rows {
				if r.Date != last {
					if i > 0 {
						a.printf("\n")
					}
					a.printf("%s\n", a.paint(bold, longDay(r.Date)))
					last = r.Date
				}
				lines := strings.Split(clean(r.Text), "\n")
				a.printf("  %s: %s\n", flat(r.Track), lines[0])
				for _, l := range lines[1:] {
					a.printf("    %s\n", l)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "", "first day (default: 6 days before --to)")
	cmd.Flags().StringVar(&toS, "to", "", "last day (default: today)")
	return cmd
}

func (a *App) weekCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "week",
		Short: "The last 7 days of every track, today first, with the current run",
		Args:  args(0, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			today := a.today()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			if !c.Has(tickbuddy.FeatureStats) {
				return tickbuddy.NeedsNewerServer(tickbuddy.FeatureStats)
			}
			tracks, err := c.Tracks(ctx, false)
			if err != nil {
				return err
			}
			ticks, err := c.Ticks(ctx, today.AddDays(-6), today)
			if err != nil {
				return err
			}
			stats, err := c.Stats(ctx, today, 0, false, false)
			if err != nil {
				return err
			}
			statsByID := map[int]*tickbuddy.Stats{}
			for i := range stats {
				statsByID[stats[i].TrackID] = &stats[i]
			}
			j := journal{values: map[trackDay]tickbuddy.Value{}}
			for _, t := range ticks {
				j.values[trackDay{t.TrackID, t.Date}] = t.Value
			}
			days := make([]tickbuddy.Date, 7)
			for i := range days {
				days[i] = today.AddDays(-i)
			}

			type dayCell struct {
				Date  tickbuddy.Date  `json:"date"`
				Value tickbuddy.Value `json:"value"`
			}
			type row struct {
				Track         string         `json:"track"`
				TrackID       int            `json:"trackId"`
				Days          []dayCell      `json:"days"`
				Current       *tickbuddy.Run `json:"current"`
				UpToYesterday bool           `json:"upToYesterday"`
				stats         *tickbuddy.Stats
				newer         bool
			}
			rows := []row{}
			for _, t := range a.visible(tracks) {
				r := row{Track: t.Name, TrackID: t.ID, stats: statsByID[t.ID], newer: !t.KnownType()}
				for _, d := range days {
					r.Days = append(r.Days, dayCell{d, j.value(t, d)})
				}
				if r.stats != nil {
					r.Current, r.UpToYesterday = r.stats.Current, r.stats.UpToYesterday
				}
				rows = append(rows, r)
			}

			switch a.mode() {
			case jsonMode:
				return a.writeJSON(map[string]any{"today": today, "tracks": rows})
			case plain:
				for _, r := range rows {
					f := []string{flat(r.Track)}
					for _, c := range r.Days {
						f = append(f, flat(c.Value.String()))
					}
					f = append(f, runPlain(r.stats))
					a.printf("%s\n", strings.Join(f, "\t"))
				}
				return nil
			}
			w := 0
			for _, r := range rows {
				w = max(w, width(flat(r.Track)))
			}
			w += 2
			// Day columns fit the weekday headings and the widest value; a
			// value of a track of a newer type is cut to fit them instead.
			col := 2
			for _, r := range rows {
				for _, c := range r.Days {
					if !r.newer {
						col = max(col, width(symbol(c.Value)))
					}
				}
			}
			var head strings.Builder
			for _, d := range days {
				head.WriteString(pad(d.Weekday().String()[:2], col+1))
			}
			a.printf("%s%s\n", strings.Repeat(" ", w), a.paint(dim, strings.TrimRight(head.String(), " ")))
			for _, r := range rows {
				var line strings.Builder
				line.WriteString(pad(flat(r.Track), w))
				for _, c := range r.Days {
					sym := cell(c.Value, col)
					line.WriteString(a.paintSymbol(sym) + strings.Repeat(" ", col+1-width(sym)))
				}
				line.WriteString(" " + runText(r.stats) + a.newerLabel(r.newer))
				a.printf("%s\n", line.String())
			}
			return nil
		},
	}
}

func (a *App) statsCmd() *cobra.Command {
	var fromS, toS, yearS string
	cmd := &cobra.Command{
		Use:   "stats [track] [--from date] [--to date] [--year YYYY]",
		Short: "Totals, weekly mean, streaks and breaks, as on Analytics",
		Long: `Totals, weekly mean, streaks and breaks, as on Analytics: of all time, or of
a period with --from and --to (either alone) or --year. A period needs the
server's period stats (statsPeriod).`,
		Example: `  tickcli stats
  tickcli stats Exercise --from -30
  tickcli stats Exercise --year 2025
  tickcli stats Exercise --year 2026`,
		Args: args(0, 1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			today := a.today()
			from, to, err := period(fromS, toS, yearS, today)
			if err != nil {
				return err
			}
			hasPeriod := !from.IsZero() || !to.IsZero()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			if !c.Has(tickbuddy.FeatureStats) {
				return tickbuddy.NeedsNewerServer(tickbuddy.FeatureStats)
			}
			if hasPeriod && !c.Has(tickbuddy.FeatureStatsPeriod) {
				return tickbuddy.NeedsNewerServer(tickbuddy.FeatureStatsPeriod)
			}
			var tracks []tickbuddy.Track
			trackID := 0
			if len(args) == 1 {
				all, err := c.Tracks(ctx, true)
				if err != nil {
					return err
				}
				t, err := tickbuddy.Resolve(all, args[0], a.interactive())
				if err != nil {
					return err
				}
				tracks, trackID = []tickbuddy.Track{t}, t.ID
			} else {
				all, err := c.Tracks(ctx, a.opts.archived)
				if err != nil {
					return err
				}
				tracks = a.listed(all)
			}
			var stats []tickbuddy.Stats
			if hasPeriod {
				stats, err = c.StatsPeriod(ctx, today, from, to, trackID, false, a.opts.archived && trackID == 0)
			} else {
				stats, err = c.Stats(ctx, today, trackID, false, a.opts.archived && trackID == 0)
			}
			if err != nil {
				return err
			}
			statsByID := map[int]tickbuddy.Stats{}
			for _, s := range stats {
				statsByID[s.TrackID] = s
			}

			type out struct {
				Track string          `json:"track"`
				From  *tickbuddy.Date `json:"from,omitempty"`
				To    *tickbuddy.Date `json:"to,omitempty"`
				tickbuddy.Stats
			}
			given := func(d tickbuddy.Date) *tickbuddy.Date {
				if d.IsZero() {
					return nil
				}
				return &d
			}
			var rows []out
			for _, t := range tracks {
				if s, ok := statsByID[t.ID]; ok {
					rows = append(rows, out{t.Name, given(from), given(to), s})
				}
			}

			switch a.mode() {
			case jsonMode:
				if rows == nil {
					rows = []out{}
				}
				return a.writeJSON(rows)
			case plain:
				if trackID != 0 {
					for _, r := range rows {
						a.statsPlain(r.Track, from, to, &r.Stats)
					}
					return nil
				}
				for _, r := range rows {
					a.printf("%s\t%d\t%.1f\t%s\n", flat(r.Track), r.Total, r.WeeklyMean, runPlain(&r.Stats))
				}
				return nil
			}
			if trackID != 0 {
				for _, r := range rows {
					heading := flat(r.Track)
					if hasPeriod {
						heading += ", " + periodText(from, to)
					}
					a.statsCard(heading, &r.Stats, !to.IsZero() && to.Before(today))
				}
				return nil
			}
			w := 0
			for _, r := range rows {
				w = max(w, width(flat(r.Track)))
			}
			for _, r := range rows {
				a.printf("%s  %6d total  %5.1f/week  %s\n", pad(flat(r.Track), w), r.Total, r.WeeklyMean, runText(&r.Stats))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&fromS, "from", "", "first day of the period (default: the first tick)")
	cmd.Flags().StringVar(&toS, "to", "", "last day of the period (default: today)")
	cmd.Flags().StringVar(&yearS, "year", "", "the period 1 Jan to 31 Dec of a year (YYYY)")
	return cmd
}

var yearPattern = regexp.MustCompile(`^[0-9]{4}$`)

// period reads stats' --from, --to and --year; zero dates where none is
// given.
func period(fromS, toS, yearS string, today tickbuddy.Date) (from, to tickbuddy.Date, err error) {
	if yearS != "" {
		if fromS != "" || toS != "" {
			return from, to, usagef("Use --year or --from/--to, not both")
		}
		if !yearPattern.MatchString(yearS) {
			return from, to, usagef("--year %q isn't a year; give four digits, as in 2025", yearS)
		}
		y, _ := strconv.Atoi(yearS)
		from, err = tickbuddy.NewDate(y, time.January, 1)
		if err != nil {
			return from, to, usagef("--year %q isn't a year; give four digits, as in 2025", yearS)
		}
		to, _ = tickbuddy.NewDate(y, time.December, 31)
		return from, to, nil
	}
	if fromS != "" {
		if from, err = tickbuddy.ParseDate(fromS, today); err != nil {
			return from, to, err
		}
	}
	if toS != "" {
		if to, err = tickbuddy.ParseDate(toS, today); err != nil {
			return from, to, err
		}
	}
	if !from.IsZero() && !to.IsZero() && to.Before(from) {
		return from, to, usagef("--from must be on or before --to")
	}
	return from, to, nil
}

// periodText words a period for a heading: "1 Jan – 31 Dec 2025".
func periodText(from, to tickbuddy.Date) string {
	switch {
	case to.IsZero():
		return "since " + dayMonthYear(from)
	case from.IsZero():
		return "up to " + dayMonthYear(to)
	case from == to:
		return dayMonthYear(to)
	case from.Year() == to.Year():
		return dayMonth(from) + " – " + dayMonthYear(to)
	}
	return dayMonthYear(from) + " – " + dayMonthYear(to)
}

func (a *App) statsPlain(name string, from, to tickbuddy.Date, s *tickbuddy.Stats) {
	kv := [][2]string{{"track", flat(name)}}
	if !from.IsZero() {
		kv = append(kv, [2]string{"from", from.String()})
	}
	if !to.IsZero() {
		kv = append(kv, [2]string{"to", to.String()})
	}
	kv = append(kv, [][2]string{
		{"total", fmt.Sprint(s.Total)},
		{"weeklyMean", fmt.Sprintf("%.1f", s.WeeklyMean)},
		{"last7", fmt.Sprint(s.Last7)},
		{"previous7", fmt.Sprint(s.Previous7)},
		{"peak7", fmt.Sprint(s.Peak7)},
		{"current", runPlain(s)},
		{"upToYesterday", fmt.Sprint(s.UpToYesterday)},
	}...)
	for _, r := range []struct {
		key string
		run *tickbuddy.Run
	}{{"longestStreak", s.LongestStreak}, {"longestBreak", s.LongestBreak}} {
		if r.run != nil {
			kv = append(kv, [2]string{r.key, fmt.Sprint(r.run.Length)}, [2]string{r.key + "From", r.run.From.String()}, [2]string{r.key + "To", r.run.To.String()})
		} else {
			kv = append(kv, [2]string{r.key, "0"})
		}
	}
	for _, d := range []struct {
		key string
		d   *tickbuddy.Date
	}{{"firstTick", s.FirstTick}, {"lastTick", s.LastTick}} {
		if d.d != nil {
			kv = append(kv, [2]string{d.key, d.d.String()})
		}
	}
	kv = append(kv, [2]string{"maxValue", fmt.Sprint(s.MaxValue)}, [2]string{"minValue", fmt.Sprint(s.MinValue)})
	for _, p := range kv {
		a.printf("%s\t%s\n", p[0], p[1])
	}
}

// statsCard prints one track's cards under heading. ended is a period that
// ended before today: the server works the run out as of its last day.
func (a *App) statsCard(heading string, s *tickbuddy.Stats, ended bool) {
	a.printf("%s\n", a.paint(bold, heading))
	a.printf("  %-18s%-9d%-14s%.1f\n", "Total", s.Total, "Weekly mean", s.WeeklyMean)
	a.printf("  %-18s%-9d%-14s%d\n", "Last 7 days", s.Last7, "Previous 7", s.Previous7)

	if ended {
		value, note := "none", ""
		if c := s.Current; c != nil {
			kind := "streak"
			if !c.IsStreak {
				kind = "break"
			}
			value = fmt.Sprintf("%d-day %s", c.Length, kind)
			if s.UpToYesterday {
				note = "(to " + dayMonth(c.To) + ")"
			}
		}
		a.cardLine("At the end", value, note)
		a.cardRun("Longest streak", s.LongestStreak)
		a.cardRun("Longest break", s.LongestBreak)
		return
	}
	label, value, note := "Current streak", "none", ""
	if c := s.Current; c != nil {
		if !c.IsStreak {
			label = "Current break"
		}
		value = plural(c.Length, "day", "days")
		switch {
		case s.UpToYesterday:
			note = "(up to yesterday)"
		case c.From == c.To:
			note = "(today)"
		default:
			note = "(since " + dayMonth(c.From) + ")"
		}
	}
	a.cardLine(label, value, note)
	a.cardRun("Longest streak", s.LongestStreak)
	a.cardRun("Longest break", s.LongestBreak)
}

func (a *App) cardRun(label string, r *tickbuddy.Run) {
	if r == nil {
		a.cardLine(label, "none", "")
		return
	}
	from := dayMonth(r.From)
	if r.From.Year() != r.To.Year() {
		from = dayMonthYear(r.From)
	}
	span := "(" + dayMonthYear(r.To) + ")"
	if r.From != r.To {
		span = "(" + from + " – " + dayMonthYear(r.To) + ")"
	}
	a.cardLine(label, plural(r.Length, "day", "days"), span)
}

func (a *App) cardLine(label, value, note string) {
	if note == "" {
		a.printf("  %-18s%s\n", label, value)
		return
	}
	a.printf("  %-18s%s%s\n", label, pad(value, 8)+" ", a.paint(dim, note))
}

func (a *App) tracksCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tracks",
		Short: "List tracks with their ids (--archived, --private to add those)",
		Args:  args(0, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := a.connect(ctx)
			if err != nil {
				return err
			}
			all, err := c.Tracks(ctx, a.opts.archived)
			if err != nil {
				return err
			}
			tracks := a.listed(all)
			slices.SortStableFunc(tracks, func(x, y tickbuddy.Track) int {
				switch {
				case x.IsArchived() == y.IsArchived():
					return 0
				case x.IsArchived():
					return 1
				}
				return -1
			})
			markers := func(t tickbuddy.Track) []string {
				var m []string
				if t.Private {
					m = append(m, "private")
				}
				if t.IsArchived() {
					m = append(m, "archived")
				}
				return m
			}
			switch a.mode() {
			case jsonMode:
				if tracks == nil {
					tracks = []tickbuddy.Track{}
				}
				return a.writeJSON(tracks)
			case plain:
				for _, t := range tracks {
					a.printf("%d\t%s\t%s\t%s\n", t.ID, flat(t.Name), t.Type, strings.Join(markers(t), ","))
				}
				return nil
			}
			wID, wName := 0, 0
			for _, t := range tracks {
				wID = max(wID, width(fmt.Sprint(t.ID))+1)
				wName = max(wName, width(flat(t.Name)))
			}
			for _, t := range tracks {
				line := fmt.Sprintf("  %s  %s  %s", pad(fmt.Sprintf("#%d", t.ID), wID), pad(flat(t.Name), wName), pad(typeName(t), 7))
				if m := markers(t); len(m) > 0 {
					line += "  " + a.paint(dim, strings.Join(m, ", "))
				}
				a.printf("%s\n", strings.TrimRight(line, " "))
			}
			return nil
		},
	}
}

func (a *App) exportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "export [--private]",
		Short: "Write the server's JSON export to stdout, unchanged",
		Args:  args(0, 0),
		RunE: func(cmd *cobra.Command, args []string) error {
			raw, err := a.client().Export(cmd.Context(), a.opts.private)
			if err != nil {
				return err
			}
			if _, err := a.Stdout.Write(raw); err != nil {
				return err
			}
			_, err = a.Stdout.Write([]byte("\n"))
			return err
		},
	}
}
