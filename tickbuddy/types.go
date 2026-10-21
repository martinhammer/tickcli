// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"bytes"
	"encoding/json"
	"strconv"
)

// Track types as the server names them. A newer server may have others:
// Track.Type keeps whatever it sends, and Track.KnownType tells them apart.
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

// KnownType reports whether the track is of a type this package knows
// (TypeYesNo or TypeCounter). A track of another type can be read, its
// values kept as the server sent them, and its notes written, but not
// ticked: refuse that with NeedsNewerClient.
func (t Track) KnownType() bool { return t.Type == TypeYesNo || t.Type == TypeCounter }

// IsArchived reports whether the track is archived.
func (t Track) IsArchived() bool { return t.ArchivedAt != nil && !t.ArchivedAt.IsZero() }

// Tick mirrors the server's TickbuddyTick: a day with a tick (yes/no tracks,
// Value 1) or a count above zero (counters). Days without one aren't listed.
// Value is as the server sent it: a number for both known types (ValueFor
// makes it the track's), or any JSON on a track of a newer type.
type Tick struct {
	TrackID int   `json:"trackId"`
	Date    Date  `json:"date"`
	Value   Value `json:"value"`
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
// on a counter. It marshals to a JSON boolean or number accordingly. Any
// other JSON, as a track of a newer type may hold, is kept as sent: see Raw.
type Value struct {
	isBool bool
	n      int
	raw    string // the JSON of any other value, as sent
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

// rawValue is the value b, kept as JSON text.
func rawValue(b []byte) Value {
	var c bytes.Buffer
	if err := json.Compact(&c, b); err != nil {
		return Value{raw: string(b)}
	}
	return Value{raw: c.String()}
}

// ValueFor is the tick's stored value v as a day's value on t: yes/no or a
// count. On a track of a type this package doesn't know, it is v as JSON
// (Raw), unchanged.
func ValueFor(t Track, v Value) Value {
	switch {
	case v.raw != "":
		return v
	case !t.KnownType():
		b, _ := v.MarshalJSON()
		return Value{raw: string(b)}
	case t.IsCounter():
		return IntValue(v.n)
	}
	return BoolValue(v.n > 0)
}

// EmptyValue is the value of a day without a tick on t: no, 0, or JSON
// null on a track of a type this package doesn't know.
func EmptyValue(t Track) Value {
	switch {
	case !t.KnownType():
		return Value{raw: "null"}
	case t.IsCounter():
		return IntValue(0)
	}
	return BoolValue(false)
}

// IsBool reports whether this is a yes/no value.
func (v Value) IsBool() bool { return v.isBool }

// Bool is the yes/no state; for a count, whether it is above zero; false for
// a Raw value.
func (v Value) Bool() bool { return v.n > 0 }

// Int is the count; for a yes/no value, 1 or 0; 0 for a Raw value.
func (v Value) Int() int { return v.n }

// Raw is the value as the server sent it when it is neither a boolean nor a
// whole number (7.5, "red", an object, null), or any value on a track of a
// type this package doesn't know (see ValueFor); nil otherwise.
func (v Value) Raw() json.RawMessage {
	if v.raw == "" {
		return nil
	}
	return json.RawMessage(v.raw)
}

// String is yes/no, the count, or the JSON of a Raw value.
func (v Value) String() string {
	switch {
	case v.raw != "":
		return v.raw
	case v.isBool && v.Bool():
		return "yes"
	case v.isBool:
		return "no"
	}
	return strconv.Itoa(v.n)
}

// MarshalJSON writes true/false, a number, or a Raw value as it came.
func (v Value) MarshalJSON() ([]byte, error) {
	switch {
	case v.raw != "":
		return []byte(v.raw), nil
	case v.isBool:
		return json.Marshal(v.Bool())
	}
	return json.Marshal(v.n)
}

// UnmarshalJSON reads true/false or a whole number, and keeps any other
// JSON as Raw, so a value of a newer track type never fails a decode.
func (v *Value) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch s := string(b); {
	case s == "true" || s == "false":
		*v = BoolValue(s == "true")
	default:
		if n, err := strconv.Atoi(s); err == nil {
			*v = IntValue(n)
		} else {
			*v = rawValue(b)
		}
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
