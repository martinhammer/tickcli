// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
)

// Track types as the server names them. A newer server may have others:
// Track.Type keeps whatever it sends, and Track.Known tells them apart.
const (
	TypeYesNo   = "boolean"
	TypeCounter = "counter"
	TypeNumber  = "number"
	TypeChoice  = "choice"
)

// Track mirrors the server's TickbuddyTrack. The server sends the five
// settings on every track, null where the type has none; they decode as the
// zero value then, and only the track's type says which of them mean
// anything.
type Track struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	SortOrder  int    `json:"sortOrder"`
	Private    bool   `json:"private"`
	ArchivedAt *Date  `json:"archivedAt"`

	// Unit is a Number track's unit, "" when it has none.
	Unit string `json:"unit"`
	// DecimalPlaces is how many decimals a Number track shows, 0 to 3; the
	// server rounds what is sent to them.
	DecimalPlaces int `json:"decimalPlaces"`
	// AllowNegative is whether a Number track takes values below 0.
	AllowNegative bool `json:"allowNegative"`
	// Options are a Choice track's options, in place order. They are fixed
	// once the track exists: only their labels change.
	Options []ChoiceOption `json:"options"`
	// OrderedScale is whether a Choice track's options run from one end of
	// a scale to the other, rather than being a plain list.
	OrderedScale bool `json:"orderedScale"`
}

// ChoiceOption is one of a Choice track's options.
type ChoiceOption struct {
	Value int    `json:"value"` // the place, 1 to n: what a day stores
	Label string `json:"label"`
}

// IsCounter reports whether the track counts rather than ticks yes/no.
func (t Track) IsCounter() bool { return t.Type == TypeCounter }

// IsNumber reports whether the track holds a reading, a Decimal, each day.
func (t Track) IsNumber() bool { return t.Type == TypeNumber }

// IsChoice reports whether the track holds one of its Options each day.
func (t Track) IsChoice() bool { return t.Type == TypeChoice }

// Known reports whether the track is of a type this package knows
// (TypeYesNo, TypeCounter, TypeNumber or TypeChoice). A track of another
// type can be read, its values kept as the server sent them, and its notes
// written, but not ticked: refuse that with NeedsNewerClient.
func (t Track) Known() bool {
	switch t.Type {
	case TypeYesNo, TypeCounter, TypeNumber, TypeChoice:
		return true
	}
	return false
}

// IsArchived reports whether the track is archived.
func (t Track) IsArchived() bool { return t.ArchivedAt != nil && !t.ArchivedAt.IsZero() }

// Option is the Choice track's option in place, if it has one.
func (t Track) Option(place int) (ChoiceOption, bool) {
	for _, o := range t.Options {
		if o.Value == place {
			return o, true
		}
	}
	return ChoiceOption{}, false
}

// MarshalJSON writes the track as the server does, except that the settings
// appear only on the type that has them: a Number's unit (left out when
// empty), decimalPlaces and allowNegative, a Choice's options and
// orderedScale.
func (t Track) MarshalJSON() ([]byte, error) {
	out := struct {
		ID            int            `json:"id"`
		Name          string         `json:"name"`
		Type          string         `json:"type"`
		SortOrder     int            `json:"sortOrder"`
		Private       bool           `json:"private"`
		ArchivedAt    *Date          `json:"archivedAt"`
		Unit          string         `json:"unit,omitempty"`
		DecimalPlaces *int           `json:"decimalPlaces,omitempty"`
		AllowNegative *bool          `json:"allowNegative,omitempty"`
		Options       []ChoiceOption `json:"options,omitempty"`
		OrderedScale  *bool          `json:"orderedScale,omitempty"`
	}{ID: t.ID, Name: t.Name, Type: t.Type, SortOrder: t.SortOrder, Private: t.Private, ArchivedAt: t.ArchivedAt}
	switch {
	case t.IsNumber():
		out.Unit, out.DecimalPlaces, out.AllowNegative = t.Unit, &t.DecimalPlaces, &t.AllowNegative
	case t.IsChoice():
		out.Options, out.OrderedScale = t.Options, &t.OrderedScale
	}
	return json.Marshal(out)
}

// Tick mirrors the server's TickbuddyTick: a day with a tick (yes/no tracks,
// Value 1), a count above zero (counters), a reading (Number, 0 included) or
// an option's place (Choice). Days without one aren't listed. Value is as the
// server sent it: ValueFor makes it the track's.
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
// On Number and Choice tracks the counts are of days with an entry.
// MaxValue and MinValue are the highest and lowest count on a counter (1 or
// 0 on a yes/no track), entry on a Number, place on an ordered Choice, and
// null on a plain list; as sent, so ValueFor makes them the track's.
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
	MaxValue      Value   `json:"maxValue"`
	MinValue      Value   `json:"minValue"`
	Runs          []Run   `json:"runs,omitempty"`
}

// Value is a day's value on a track: true/false on a yes/no track, a count
// on a counter, a Decimal on a Number, an option's place on a Choice, or
// null for a day without a value. It marshals to JSON accordingly. Any other
// JSON, as a track of a newer type may hold, is kept as sent: see Raw.
type Value struct {
	kind valueKind
	n    int     // a count, a place, or 1/0 for yes/no
	d    Decimal // a Number
	raw  string  // the JSON of any other value, as sent
}

type valueKind uint8

const (
	kindInt valueKind = iota
	kindBool
	kindNumber
	kindNull
	kindRaw
)

// BoolValue is a yes/no value.
func BoolValue(b bool) Value {
	if b {
		return Value{kind: kindBool, n: 1}
	}
	return Value{kind: kindBool}
}

// IntValue is a counter value, or a Choice's place.
func IntValue(n int) Value { return Value{n: n} }

// NumberValue is a Number track's value.
func NumberValue(d Decimal) Value { return Value{kind: kindNumber, d: d} }

// NullValue is a day without a value on a Number or Choice track, or on a
// track of a type this package doesn't know.
func NullValue() Value { return Value{kind: kindNull} }

// rawValue is the value b, kept as JSON text.
func rawValue(b []byte) Value {
	var c bytes.Buffer
	if err := json.Compact(&c, b); err != nil {
		return Value{kind: kindRaw, raw: string(b)}
	}
	return Value{kind: kindRaw, raw: c.String()}
}

// asRaw is v as JSON, kept as Raw.
func (v Value) asRaw() Value {
	if v.kind == kindRaw {
		return v
	}
	b, _ := v.MarshalJSON()
	return Value{kind: kindRaw, raw: string(b)}
}

// ValueFor is the tick's stored value v as a day's value on t: yes/no, a
// count, a Number's Decimal or a Choice's place; null stays null. A value
// the track's type can't hold (a fraction on a Choice, more than three
// decimals on a Number; only a bug can send one), and any value on a track
// of a type this package doesn't know, is v as JSON (Raw), unchanged.
func ValueFor(t Track, v Value) Value {
	switch {
	case v.kind == kindNull:
		return v
	case !t.Known():
		return v.asRaw()
	case t.IsNumber():
		switch v.kind {
		case kindNumber:
			return v
		case kindInt:
			if m := int64(v.n); m <= math.MaxInt64/1000 && m >= -math.MaxInt64/1000 {
				return NumberValue(Decimal{m * 1000})
			}
		case kindRaw:
			if d, err := ParseDecimal(v.raw); err == nil {
				return NumberValue(d)
			}
		}
		return v.asRaw()
	case t.IsChoice():
		if v.kind == kindInt {
			return v
		}
		return v.asRaw()
	case v.kind == kindRaw:
		return v
	case t.IsCounter():
		return IntValue(v.n)
	}
	return BoolValue(v.n > 0)
}

// EmptyValue is the value of a day without a tick on t: no, 0, or null on a
// Number or Choice track and on a track of a type this package doesn't know.
func EmptyValue(t Track) Value {
	switch {
	case !t.Known(), t.IsNumber(), t.IsChoice():
		return NullValue()
	case t.IsCounter():
		return IntValue(0)
	}
	return BoolValue(false)
}

// IsBool reports whether this is a yes/no value.
func (v Value) IsBool() bool { return v.kind == kindBool }

// IsNull reports whether this is null: a day without a value.
func (v Value) IsNull() bool { return v.kind == kindNull }

// Bool is the yes/no state; for a count or a place, whether it is above
// zero; false for anything else.
func (v Value) Bool() bool { return v.n > 0 }

// Int is the count or a Choice's place; for a yes/no value, 1 or 0; 0 for
// anything else.
func (v Value) Int() int { return v.n }

// Decimal is a Number track's value, as ValueFor makes it.
func (v Value) Decimal() (Decimal, bool) { return v.d, v.kind == kindNumber }

// Raw is the value as the server sent it when it is neither a boolean, a
// whole number nor null (7.5, "red", an object), or any value but null on a
// track of a type this package doesn't know (see ValueFor); nil otherwise.
// ValueFor makes a Number's 7.5 a Decimal.
func (v Value) Raw() json.RawMessage {
	if v.kind != kindRaw {
		return nil
	}
	return json.RawMessage(v.raw)
}

// String is yes/no, the count or place, a Number's Decimal in its shortest
// exact form, "" for null, or the JSON of a Raw value.
func (v Value) String() string {
	switch v.kind {
	case kindRaw:
		return v.raw
	case kindNull:
		return ""
	case kindNumber:
		return v.d.String()
	case kindBool:
		if v.Bool() {
			return "yes"
		}
		return "no"
	}
	return strconv.Itoa(v.n)
}

// MarshalJSON writes true/false, a number, null, or a Raw value as it came.
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.kind {
	case kindRaw:
		return []byte(v.raw), nil
	case kindNull:
		return []byte("null"), nil
	case kindNumber:
		return v.d.MarshalJSON()
	case kindBool:
		return json.Marshal(v.Bool())
	}
	return json.Marshal(v.n)
}

// UnmarshalJSON reads true/false, a whole number or null, and keeps any
// other JSON as Raw, so a value of a newer track type never fails a decode.
func (v *Value) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch s := string(b); {
	case s == "true" || s == "false":
		*v = BoolValue(s == "true")
	case s == "null":
		*v = NullValue()
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
	// ErrReadOnly instead. Number and Choice writes never set it: every
	// server with those types honours it, and the server rounds Numbers, so
	// a different answer means nothing there.
	Archived bool
}
