// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// allTypes asks a server to include tracks of types this package doesn't
// know, which it leaves out of /tracks, /ticks and /notes otherwise; an
// older server ignores it. Track.KnownType tells them apart.
func allTypes() url.Values { return url.Values{"includeAllTypes": {"true"}} }

// Tracks lists the tracks in journal order, of every type (see
// Track.KnownType). Archived tracks are included only when includeArchived
// is set and the server has archivedTracks (an older server has no archived
// tracks to show).
func (c *Client) Tracks(ctx context.Context, includeArchived bool) ([]Track, error) {
	q := allTypes()
	if includeArchived {
		ok, err := c.has(ctx, FeatureArchivedTracks)
		if err != nil {
			return nil, err
		}
		if ok {
			q.Set("includeArchived", "true")
		}
	}
	var tracks []Track
	err := c.do(ctx, request{method: http.MethodGet, path: "/tracks", query: q, retry: true}, &tracks)
	return tracks, err
}

// Ticks lists the ticks from from to to, both included, archived tracks'
// and those of every type too.
func (c *Client) Ticks(ctx context.Context, from, to Date) ([]Tick, error) {
	q := allTypes()
	q.Set("from", from.String())
	q.Set("to", to.String())
	var ticks []Tick
	err := c.do(ctx, request{method: http.MethodGet, path: "/ticks", query: q, retry: true}, &ticks)
	return ticks, err
}

// NoteEdit is a change to the day's note sent with a tick call: Text
// replaces the note ("" deletes it), or with Append is added to it on a new
// line, as AppendNote does. AllowDuplicate appends even text the note
// already holds.
type NoteEdit struct {
	Text           string
	Append         bool
	AllowDuplicate bool
}

// ReplaceNote is the NoteEdit that replaces the note with text.
func ReplaceNote(text string) *NoteEdit { return &NoteEdit{Text: text} }

// AppendNote is the NoteEdit that appends text to the note.
func AppendNote(text string) *NoteEdit { return &NoteEdit{Text: text, Append: true} }

// noteFields are a tick call's note fields; nil sends none.
type noteFields struct {
	Note               *string `json:"note,omitempty"`
	AppendNote         *string `json:"appendNote,omitempty"`
	AllowDuplicateNote bool    `json:"allowDuplicateNote,omitempty"`
}

func (e *NoteEdit) fields() noteFields {
	switch {
	case e == nil:
		return noteFields{}
	case e.Append:
		return noteFields{AppendNote: &e.Text, AllowDuplicateNote: e.AllowDuplicate}
	}
	return noteFields{Note: &e.Text}
}

// repeatable reports whether a call sending e may be repeated: an append
// with AllowDuplicate would add its line twice.
func (e *NoteEdit) repeatable() bool { return e == nil || !e.Append || !e.AllowDuplicate }

// checkNote fails unless the server takes e: notes need the notes flag,
// appends notesAppend too.
func (c *Client) checkNote(ctx context.Context, e *NoteEdit) error {
	if e == nil {
		return nil
	}
	if err := c.need(ctx, FeatureNotes); err != nil {
		return err
	}
	if e.Append {
		return c.need(ctx, FeatureNotesAppend)
	}
	return nil
}

// editNote applies e on its own, after a tick call that couldn't carry it.
func (c *Client) editNote(ctx context.Context, trackID int, date Date, e *NoteEdit) (string, error) {
	if e.Append {
		return c.AppendNote(ctx, trackID, date, e.Text, e.AllowDuplicate)
	}
	return c.SetNote(ctx, trackID, date, e.Text)
}

// appendAfterConflict handles a tick call's error. A 409 with an append
// means the tick was written but the note kept changing, so the tick call
// isn't repeated (a repeated Add would count twice): the text is appended
// with AppendNote instead, and its answer returned. ok is false for any
// other error, which is returned unchanged.
func (c *Client) appendAfterConflict(ctx context.Context, err error, trackID int, date Date, e *NoteEdit) (note string, ok bool, _ error) {
	if e == nil || !e.Append || !isConflict(err) {
		return "", false, err
	}
	note, err = c.AppendNote(ctx, trackID, date, e.Text, e.AllowDuplicate)
	return note, err == nil, err
}

type setBody struct {
	TrackID int    `json:"trackId"`
	Date    string `json:"date"`
	Value   any    `json:"value"`
	noteFields
}

type valueAnswer struct {
	Value Value   `json:"value"`
	Note  *string `json:"note"`
}

// SetYesNo ticks (ticked=true) or unticks a yes/no track on date. note
// replaces or appends to the day's note; nil leaves it alone.
//
// Without the server's booleanSet flag it reads the day and toggles only if
// the state differs; that toggle is never retried.
func (c *Client) SetYesNo(ctx context.Context, trackID int, date Date, ticked bool, note *NoteEdit) (Result, error) {
	if err := c.checkNote(ctx, note); err != nil {
		return Result{}, err
	}
	want := BoolValue(ticked)
	hasSet, err := c.has(ctx, FeatureBooleanSet)
	if err != nil {
		return Result{}, err
	}
	if hasSet {
		var a valueAnswer
		body := setBody{TrackID: trackID, Date: date.String(), Value: ticked, noteFields: note.fields()}
		err := c.do(ctx, request{method: http.MethodPost, path: "/ticks/set", body: body, retry: note.repeatable()}, &a)
		if text, ok, err := c.appendAfterConflict(ctx, err, trackID, date, note); ok {
			return result(want, &text, want, note), nil
		} else if err != nil {
			return Result{}, err
		}
		return result(BoolValue(a.Value.Bool()), a.Note, want, note), nil
	}

	cur, err := c.dayValue(ctx, trackID, date)
	if err != nil {
		return Result{}, err
	}
	if (cur > 0) == ticked {
		if note == nil {
			return Result{Value: want}, nil
		}
		text, err := c.editNote(ctx, trackID, date, note)
		if err != nil {
			return Result{}, err
		}
		return result(want, &text, want, note), nil
	}
	var a struct {
		Ticked bool    `json:"ticked"`
		Note   *string `json:"note"`
	}
	body := struct {
		TrackID int    `json:"trackId"`
		Date    string `json:"date"`
		noteFields
	}{trackID, date.String(), note.fields()}
	err = c.do(ctx, request{method: http.MethodPost, path: "/ticks/toggle", body: body}, &a)
	if text, ok, err := c.appendAfterConflict(ctx, err, trackID, date, note); ok {
		return result(want, &text, want, note), nil
	} else if err != nil {
		return Result{}, err
	}
	return result(BoolValue(a.Ticked), a.Note, want, note), nil
}

// SetCount sets a counter on date to n; 0 removes the count. note works as
// for SetYesNo.
func (c *Client) SetCount(ctx context.Context, trackID int, date Date, n int, note *NoteEdit) (Result, error) {
	if err := c.checkNote(ctx, note); err != nil {
		return Result{}, err
	}
	return c.setCount(ctx, trackID, date, n, note, note.repeatable())
}

func (c *Client) setCount(ctx context.Context, trackID int, date Date, n int, note *NoteEdit, retry bool) (Result, error) {
	var a valueAnswer
	body := setBody{TrackID: trackID, Date: date.String(), Value: n, noteFields: note.fields()}
	err := c.do(ctx, request{method: http.MethodPost, path: "/ticks/set", body: body, retry: retry}, &a)
	if text, ok, err := c.appendAfterConflict(ctx, err, trackID, date, note); ok {
		return result(IntValue(n), &text, IntValue(n), note), nil
	} else if err != nil {
		return Result{}, err
	}
	return result(IntValue(a.Value.Int()), a.Note, IntValue(n), note), nil
}

// Add adds delta (negative to take away, never below 0) to a counter on
// date. note works as for SetYesNo.
//
// Add is never retried: if the server applied a request whose answer was
// lost, a retry would count twice. Without the server's counterIncrement
// flag it reads the day and sets the sum.
//
// Result.Archived can only be told from the note, since the server answers
// an archived track with its unchanged count (a newer server fails with
// ErrReadOnly).
func (c *Client) Add(ctx context.Context, trackID int, date Date, delta int, note *NoteEdit) (Result, error) {
	if delta == 0 {
		return Result{}, &Error{Kind: ErrBadRequest, Message: "the amount to add must not be 0"}
	}
	if err := c.checkNote(ctx, note); err != nil {
		return Result{}, err
	}
	hasInc, err := c.has(ctx, FeatureCounterIncrement)
	if err != nil {
		return Result{}, err
	}
	if hasInc {
		var a valueAnswer
		body := struct {
			TrackID int    `json:"trackId"`
			Date    string `json:"date"`
			Delta   int    `json:"delta"`
			noteFields
		}{trackID, date.String(), delta, note.fields()}
		err := c.do(ctx, request{method: http.MethodPost, path: "/ticks/increment", body: body}, &a)
		if text, ok, err := c.appendAfterConflict(ctx, err, trackID, date, note); ok {
			// The 409 carries no count, so read it.
			n, err := c.dayValue(ctx, trackID, date)
			if err != nil {
				return Result{}, err
			}
			return result(IntValue(n), &text, IntValue(n), note), nil
		} else if err != nil {
			return Result{}, err
		}
		got := IntValue(a.Value.Int())
		return result(got, a.Note, got, note), nil
	}
	cur, err := c.dayValue(ctx, trackID, date)
	if err != nil {
		return Result{}, err
	}
	return c.setCount(ctx, trackID, date, max(0, cur+delta), note, false)
}

// dayValue reads one track's stored value on date (0 without a tick).
func (c *Client) dayValue(ctx context.Context, trackID int, date Date) (int, error) {
	ticks, err := c.Ticks(ctx, date, date)
	if err != nil {
		return 0, err
	}
	for _, t := range ticks {
		if t.TrackID == trackID && t.Date == date {
			return t.Value.Int(), nil
		}
	}
	return 0, nil
}

// result builds a Result, marking it archived when the server answered with
// something other than what was sent: another value, a replaced note that
// isn't the one sent, or a note that lacks the appended lines.
func result(got Value, gotNote *string, want Value, sent *NoteEdit) Result {
	r := Result{Value: got, Note: gotNote}
	if got != want {
		r.Archived = true
	}
	if sent != nil {
		if gotNote == nil {
			empty := ""
			r.Note = &empty
		}
		if sent.Append && !NoteContains(*r.Note, sent.Text) || !sent.Append && *r.Note != NormalizeNote(sent.Text) {
			r.Archived = true
		}
	}
	return r
}

// NormalizeNote returns text as the server stores it: \r\n and \r become \n,
// and surrounding white space is trimmed (as PHP's trim does).
func NormalizeNote(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Trim(text, " \t\n\r\x00\x0b")
}

// NoteContains reports whether note holds the lines of text (normalised as
// by NormalizeNote) in order, as whole consecutive lines: the server's test
// for text already appended. Empty text is always contained.
func NoteContains(note, text string) bool {
	text = NormalizeNote(text)
	if text == "" {
		return true
	}
	lines, want := strings.Split(note, "\n"), strings.Split(text, "\n")
	for i := 0; i+len(want) <= len(lines); i++ {
		if slices.Equal(lines[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// Stats reads the stat cards for every track (trackID 0) or one track, as on
// today. includeRuns adds every streak and break. It needs the server's
// stats flag: no streak maths happens in Go.
func (c *Client) Stats(ctx context.Context, today Date, trackID int, includeRuns, includeArchived bool) ([]Stats, error) {
	if err := c.need(ctx, FeatureStats); err != nil {
		return nil, err
	}
	return c.stats(ctx, url.Values{"today": {today.String()}}, trackID, includeRuns, includeArchived)
}

// StatsPeriod is Stats over the days from from to to, both included. A zero
// from starts at the first tick and a zero to ends today. The server works
// each track out up to the earliest of to, today and its archive date, and
// cuts streaks and breaks at the period's edges. It needs the server's
// statsPeriod flag, since an older server ignores the period and answers
// all-time stats.
func (c *Client) StatsPeriod(ctx context.Context, today, from, to Date, trackID int, includeRuns, includeArchived bool) ([]Stats, error) {
	if err := c.need(ctx, FeatureStatsPeriod); err != nil {
		return nil, err
	}
	q := url.Values{"today": {today.String()}}
	if !from.IsZero() {
		q.Set("from", from.String())
	}
	if !to.IsZero() {
		q.Set("to", to.String())
	}
	return c.stats(ctx, q, trackID, includeRuns, includeArchived)
}

func (c *Client) stats(ctx context.Context, q url.Values, trackID int, includeRuns, includeArchived bool) ([]Stats, error) {
	if trackID != 0 {
		q.Set("trackId", strconv.Itoa(trackID))
	}
	if includeRuns {
		q.Set("includeRuns", "true")
	}
	if includeArchived {
		q.Set("includeArchived", "true")
	}
	var raw []json.RawMessage
	if err := c.do(ctx, request{method: http.MethodGet, path: "/stats", query: q, retry: true}, &raw); err != nil {
		return nil, err
	}
	// One entry at a time, so that a track whose stats don't fit Stats, as
	// a track of a newer type may give, is left out rather than failing the
	// others.
	var stats []Stats
	for _, r := range raw {
		var s Stats
		if json.Unmarshal(r, &s) == nil {
			stats = append(stats, s)
		}
	}
	return stats, nil
}

// Notes lists the notes from from to to, newest first, private and archived
// tracks' and those of every type included.
func (c *Client) Notes(ctx context.Context, from, to Date) ([]Note, error) {
	if err := c.need(ctx, FeatureNotes); err != nil {
		return nil, err
	}
	q := allTypes()
	q.Set("from", from.String())
	q.Set("to", to.String())
	var notes []Note
	err := c.do(ctx, request{method: http.MethodGet, path: "/notes", query: q, retry: true}, &notes)
	return notes, err
}

// SetNote sets or replaces the note on a track's day and returns the stored
// text. An empty text deletes the note (and returns ""), as on the server.
// On an archived track the server changes nothing and returns the stored
// note, so a result other than NormalizeNote(text) means the track is
// archived.
func (c *Client) SetNote(ctx context.Context, trackID int, date Date, text string) (string, error) {
	if err := c.need(ctx, FeatureNotes); err != nil {
		return "", err
	}
	var a struct {
		Text string `json:"text"`
	}
	body := struct {
		TrackID int    `json:"trackId"`
		Date    string `json:"date"`
		Text    string `json:"text"`
	}{trackID, date.String(), text}
	err := c.do(ctx, request{method: http.MethodPost, path: "/notes/set", body: body, retry: true}, &a)
	return a.Text, err
}

// AppendNote adds text to the note on a track's day on a new line (or
// stores it if the day has none) and returns the whole stored note. Text
// whose lines the note already holds, in order and as whole lines, is
// skipped unless allowDuplicate is set, so running it again changes
// nothing; empty text changes nothing. It is retried like SetNote, also
// after a 409 (other writers kept changing the note), except with
// allowDuplicate, where a repeat would add the text twice. On an archived
// track the server changes nothing and returns the stored note, so a result
// for which NoteContains(result, text) is false means the track is archived.
func (c *Client) AppendNote(ctx context.Context, trackID int, date Date, text string, allowDuplicate bool) (string, error) {
	if err := c.checkNote(ctx, &NoteEdit{Append: true}); err != nil {
		return "", err
	}
	var a struct {
		Text string `json:"text"`
	}
	body := struct {
		TrackID        int    `json:"trackId"`
		Date           string `json:"date"`
		Text           string `json:"text"`
		AllowDuplicate bool   `json:"allowDuplicate,omitempty"`
	}{trackID, date.String(), text, allowDuplicate}
	err := c.do(ctx, request{method: http.MethodPost, path: "/notes/append", body: body, retry: !allowDuplicate, retryConflict: !allowDuplicate}, &a)
	return a.Text, err
}

// DeleteNote deletes the note on a track's day; no note is not an error.
// The server answers the same for an archived track, where it deletes
// nothing; use SetNote with "" to be able to tell.
func (c *Client) DeleteNote(ctx context.Context, trackID int, date Date) error {
	if err := c.need(ctx, FeatureNotes); err != nil {
		return err
	}
	q := url.Values{"trackId": {strconv.Itoa(trackID)}, "date": {date.String()}}
	return c.do(ctx, request{method: http.MethodDelete, path: "/notes", query: q, retry: true}, nil)
}

// CreateTrack creates a track of kind TypeYesNo or TypeCounter. It is never
// retried, since a lost answer would create the track twice.
func (c *Client) CreateTrack(ctx context.Context, name, kind string) (Track, error) {
	var t Track
	body := struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}{name, kind}
	err := c.do(ctx, request{method: http.MethodPost, path: "/tracks", body: body}, &t)
	return t, err
}

// RenameTrack renames a track and returns it.
func (c *Client) RenameTrack(ctx context.Context, trackID int, name string) (Track, error) {
	var t Track
	body := struct {
		Name string `json:"name"`
	}{name}
	err := c.do(ctx, request{method: http.MethodPut, path: "/tracks/" + strconv.Itoa(trackID), body: body, retry: true}, &t)
	return t, err
}

// Export returns the server's JSON export unchanged. Private tracks are left
// out unless includePrivate is set.
func (c *Client) Export(ctx context.Context, includePrivate bool) (json.RawMessage, error) {
	q := url.Values{}
	if includePrivate {
		q.Set("includePrivate", "true")
	}
	var raw json.RawMessage
	err := c.do(ctx, request{method: http.MethodGet, path: "/export", query: q, retry: true}, &raw)
	return raw, err
}
