// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Track types as the server names them.
const (
	TypeYesNo   = "boolean"
	TypeCounter = "counter"
)

// Track mirrors the server's TickbuddyTrack.
type Track struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	SortOrder  int    `json:"sortOrder"`
	Private    bool   `json:"private"`
	ArchivedAt *Date  `json:"archivedAt"`
}

// IsCounter reports whether the track counts rather than ticks yes/no.
func (t Track) IsCounter() bool { return t.Type == TypeCounter }

// IsArchived reports whether the track is archived.
func (t Track) IsArchived() bool { return t.ArchivedAt != nil && !t.ArchivedAt.IsZero() }

// Tick mirrors the server's TickbuddyTick: a day with a tick (yes/no tracks,
// Value 1) or a count above zero (counters). Days without one aren't listed.
type Tick struct {
	TrackID int  `json:"trackId"`
	Date    Date `json:"date"`
	Value   int  `json:"value"`
}

// Note mirrors the server's TickbuddyNote.
type Note struct {
	TrackID int    `json:"trackId"`
	Date    Date   `json:"date"`
	Text    string `json:"text"`
}

// Run mirrors the server's TickbuddyRun: a streak of ticked days or a break.
type Run struct {
	IsStreak bool `json:"isStreak"`
	Length   int  `json:"length"`
	From     Date `json:"from"`
	To       Date `json:"to"`
}

// Stats mirrors the server's TickbuddyStats, the numbers on Analytics' cards.
type Stats struct {
	TrackID       int     `json:"trackId"`
	Total         int     `json:"total"`
	WeeklyMean    float64 `json:"weeklyMean"`
	Last7         int     `json:"last7"`
	Previous7     int     `json:"previous7"`
	Peak7         int     `json:"peak7"`
	Current       *Run    `json:"current"`
	UpToYesterday bool    `json:"upToYesterday"`
	LongestStreak *Run    `json:"longestStreak"`
	LongestBreak  *Run    `json:"longestBreak"`
	FirstTick     *Date   `json:"firstTick"`
	LastTick      *Date   `json:"lastTick"`
	MaxValue      int     `json:"maxValue"`
	MinValue      int     `json:"minValue"`
	Runs          []Run   `json:"runs,omitempty"`
}

// Value is a day's value on a track: true/false on a yes/no track, a count
// on a counter. It marshals to a JSON boolean or number accordingly.
type Value struct {
	isBool bool
	n      int
}

// BoolValue is a yes/no value.
func BoolValue(b bool) Value {
	if b {
		return Value{isBool: true, n: 1}
	}
	return Value{isBool: true}
}

// IntValue is a counter value.
func IntValue(n int) Value { return Value{n: n} }

// ValueFor is the value of n (a tick's stored value, 0 for no tick) on t.
func ValueFor(t Track, n int) Value {
	if t.IsCounter() {
		return IntValue(n)
	}
	return BoolValue(n > 0)
}

// IsBool reports whether this is a yes/no value.
func (v Value) IsBool() bool { return v.isBool }

// Bool is the yes/no state; for a count, whether it is above zero.
func (v Value) Bool() bool { return v.n > 0 }

// Int is the count; for a yes/no value, 1 or 0.
func (v Value) Int() int { return v.n }

func (v Value) String() string {
	if v.isBool {
		if v.Bool() {
			return "yes"
		}
		return "no"
	}
	return strconv.Itoa(v.n)
}

// MarshalJSON writes true/false or a number.
func (v Value) MarshalJSON() ([]byte, error) {
	if v.isBool {
		return json.Marshal(v.Bool())
	}
	return json.Marshal(v.n)
}

// UnmarshalJSON reads true/false or an integer.
func (v *Value) UnmarshalJSON(b []byte) error {
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		return err
	}
	switch x := x.(type) {
	case bool:
		*v = BoolValue(x)
	case float64:
		*v = IntValue(int(x))
	default:
		return fmt.Errorf("tickbuddy: value %s is neither a boolean nor a number", b)
	}
	return nil
}

// Result is what a write returns.
type Result struct {
	// Value is the day's value after the write, as the server answered it.
	Value Value
	// Note is the whole stored note after the write, when a note was
	// replaced or appended ("" when there is none); nil when the call
	// left the note alone.
	Note *string
	// Archived is set when the server answered with a different state than
	// the one sent, which it does only for an archived track (it saves
	// nothing then and answers 200). The caller decides what that means.
	// A write that already matched the stored state can't tell. A server
	// that honours Prefer: handling=strict fails the write with
	// ErrReadOnly instead.
	Archived bool
}
