// SPDX-License-Identifier: GPL-3.0-or-later

// Package tickbuddytest is an in-memory Tickbuddy server for testing
// Tickbuddy Go clients, named after net/http/httptest. It serves the OCS
// API, Nextcloud Login Flow v2 and app password revocation, and follows the
// server's rules where tests rely on them: sparse ticks, the quiet 200 on
// archived tracks, notes outliving ticks, and feature flags switching
// endpoints off as an older server would. Track.ReadOnly gives the planned
// server's 423 instead. Number and Choice tracks follow the server too: a
// Number's value is stored in thousandths and rounded half away from zero to
// the track's decimal places, a Choice's is its option's place. A track of
// another type than "boolean" or "counter" (Number, Choice, or a newer type
// such as "future") is left out of GET /tracks, /ticks, /notes and /stats
// unless the client sends includeAllTypes=true, as the server does; SetValue
// gives a newer type's track values of any JSON.
//
// It is shared so that tickcli and TickLLMe test their sign-in against the
// same fake.
package tickbuddytest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Track is a stored track.
type Track struct {
	ID         int
	Name       string
	Type       string // "boolean", "counter", "number", "choice", or any newer type
	Private    bool
	ArchivedAt string
	// A Number's settings.
	Unit          string
	DecimalPlaces int
	AllowNegative bool
	// A Choice's settings: Options are the labels, in place order.
	Options      []string
	OrderedScale bool
	// ReadOnly makes every write to the track answer 423 Locked with this
	// reason ("archived", "immutable", …) to a client that sends Prefer:
	// handling=strict, as the planned server does; other clients get the
	// quiet 200 with nothing saved. "archived" without ArchivedAt is a track
	// archived after the client listed it.
	ReadOnly string
}

// writable reports whether writes to the track are saved.
func (t *Track) writable() bool { return t.ArchivedAt == "" && t.ReadOnly == "" }

type key struct {
	track int
	date  string
}

// Server is the fake. Lock Mu to change its fields while it runs.
type Server struct {
	*httptest.Server

	Mu       sync.Mutex
	User     string
	Password string
	Version  string
	// Features maps flags to whether they are advertised; a flag that is off
	// also switches its endpoint off (Nextcloud's own 404), as on an older server.
	Features map[string]bool
	// NoTickbuddy drops the tickbuddy capability, as without the app.
	NoTickbuddy bool
	// Disabled answers every Tickbuddy API address with Nextcloud's own 404
	// (empty data, "Invalid query…"), as while the app is disabled. The
	// capability stays unless NoTickbuddy is set too, as for a client that
	// read it before.
	Disabled bool
	// NotFoundReason goes with the "Track not found" 404 as data.reason, as
	// the planned server sends ("trackNotFound"). Today's server sends the
	// message only.
	NotFoundReason string
	// BoolSetAnswer answers set on a yes/no track with true/false, as a
	// Tickbuddy 2.3.0 pre-release build may have; the server answers 1 or 0.
	BoolSetAnswer bool
	APIVersion    int

	Tracks []Track
	// Ticks are stored values: a count, 1 for yes/no, a Number's
	// thousandths, a Choice's place.
	Ticks map[key]int
	// Values are the values of tracks of newer types, sent as they are.
	Values map[key]any
	Notes  map[key]string
	// Stats are canned answers for GET /api/stats by track id.
	Stats map[int]map[string]any
	// StatsQueries logs the query of every GET /api/stats.
	StatsQueries []url.Values

	// FailNext answers the next n API requests with Fail (502 by default)
	// before doing anything.
	FailNext int
	Fail     int
	// ApplyThenFail applies the next n writes, then answers 502, as when an
	// answer is lost on the way back.
	ApplyThenFail int

	// ConflictNext answers the next n appends (POST /notes/append, or
	// appendNote on a tick call, after its tick is written) with 409, as
	// when other writers kept changing the note.
	ConflictNext int

	// Requests logs "METHOD path" for every request, Queries its query
	// string, Bodies its body ("" without one), and Prefers its Prefer
	// header.
	Requests []string
	Queries  []string
	Bodies   []string
	Prefers  []string

	// Login flow: LoginPolls 404s before the flow answers.
	LoginPolls       int
	LoginAppPassword string
	Revoked          bool
}

// New starts a fake with every feature of the current server.
func New() *Server {
	s := &Server{
		User:     "martin",
		Password: "app-pass",
		Version:  "2.3.0",
		Features: map[string]bool{
			"import": true, "export": true, "counterTracks": true, "privateTracks": true,
			"tickBounds": true, "archivedTracks": true, "syncDelta": false,
			"counterIncrement": true, "booleanSet": true, "stats": true, "notes": true,
			"statsPeriod": true, "notesAppend": true, "numberTracks": true, "choiceTracks": true,
		},
		APIVersion:       1,
		Ticks:            map[key]int{},
		Values:           map[key]any{},
		Notes:            map[key]string{},
		Stats:            map[int]map[string]any{},
		Fail:             http.StatusBadGateway,
		LoginAppPassword: "flow-pass",
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Old switches the fake to Tickbuddy 2.2.0: none of the client API flags.
func (s *Server) Old() {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Version = "2.2.0"
	for _, f := range []string{"counterIncrement", "booleanSet", "stats", "notes", "statsPeriod", "notesAppend", "numberTracks", "choiceTracks"} {
		s.Features[f] = false
	}
}

// AddTrack adds a track and returns its id.
func (s *Server) AddTrack(name, typ string) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	id := len(s.Tracks) + 1
	s.Tracks = append(s.Tracks, Track{ID: id, Name: name, Type: typ})
	return id
}

// AddNumber adds a Number track with its unit, decimal places and whether
// it allows negative values, and returns its id.
func (s *Server) AddNumber(name, unit string, decimalPlaces int, allowNegative bool) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	id := len(s.Tracks) + 1
	s.Tracks = append(s.Tracks, Track{ID: id, Name: name, Type: "number", Unit: unit, DecimalPlaces: decimalPlaces, AllowNegative: allowNegative})
	return id
}

// AddChoice adds a Choice track with its options' labels and whether they
// are an ordered scale, and returns its id.
func (s *Server) AddChoice(name string, ordered bool, labels ...string) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	id := len(s.Tracks) + 1
	s.Tracks = append(s.Tracks, Track{ID: id, Name: name, Type: "choice", Options: labels, OrderedScale: ordered})
	return id
}

// SetTick stores a raw tick value: a count, 1 for yes/no, a Number's
// thousandths (any value), a Choice's place. 0 deletes, except on a Number.
func (s *Server) SetTick(track int, date string, v int) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if t := s.track(track); t != nil && t.Type == "number" {
		s.Ticks[key{track, date}] = v
		return
	}
	if v <= 0 {
		delete(s.Ticks, key{track, date})
	} else {
		s.Ticks[key{track, date}] = v
	}
}

// SetValue stores the value of a track of a newer type, any JSON (nil
// deletes).
func (s *Server) SetValue(track int, date string, v any) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if v == nil {
		delete(s.Values, key{track, date})
	} else {
		s.Values[key{track, date}] = v
	}
}

// Tick returns a stored tick value.
func (s *Server) Tick(track int, date string) int {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.Ticks[key{track, date}]
}

// HasTick reports whether a day has a stored tick, as a Number's 0 does.
func (s *Server) HasTick(track int, date string) bool {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	_, ok := s.Ticks[key{track, date}]
	return ok
}

// SetNote stores a note ("" deletes).
func (s *Server) SetNote(track int, date, text string) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	if text == "" {
		delete(s.Notes, key{track, date})
	} else {
		s.Notes[key{track, date}] = text
	}
}

// Note returns a stored note.
func (s *Server) Note(track int, date string) (string, bool) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	t, ok := s.Notes[key{track, date}]
	return t, ok
}

// Update changes a track under the lock.
func (s *Server) Update(id int, f func(*Track)) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	for i := range s.Tracks {
		if s.Tracks[i].ID == id {
			f(&s.Tracks[i])
		}
	}
}

// Logged returns the request log.
func (s *Server) Logged() []string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return slices.Clone(s.Requests)
}

// LoggedQueries returns the query string of every logged request ("" when
// none), in the order of Logged.
func (s *Server) LoggedQueries() []string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return slices.Clone(s.Queries)
}

// LoggedPrefers returns the Prefer header of every logged request ("" when
// not sent), in the order of Logged.
func (s *Server) LoggedPrefers() []string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return slices.Clone(s.Prefers)
}

// LoggedBodies returns the bodies of the logged requests to paths ending in
// suffix, in order.
func (s *Server) LoggedBodies(suffix string) []string {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	var out []string
	for i, r := range s.Requests {
		if strings.HasSuffix(r, suffix) {
			out = append(out, s.Bodies[i])
		}
	}
	return out
}

// Count returns how many logged requests start with prefix.
func (s *Server) Count(prefix string) int {
	n := 0
	for _, r := range s.Logged() {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func ocs(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	meta := map[string]any{"status": "ok", "statuscode": status, "message": "OK"}
	if status >= 400 {
		meta = map[string]any{"status": "failure", "statuscode": status, "message": ""}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ocs": map[string]any{"meta": meta, "data": data}})
}

func msg(w http.ResponseWriter, status int, m string) {
	ocs(w, status, map[string]string{"message": m})
}

// invalidQuery is Nextcloud's 404 for an OCS address no app serves.
func invalidQuery(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	meta := map[string]any{"status": "failure", "statuscode": 998, "message": "Invalid query, please check the syntax. API specifications are here: http://www.open-collaboration-services.org/."}
	_ = json.NewEncoder(w).Encode(map[string]any{"ocs": map[string]any{"meta": meta, "data": []any{}}})
}

// trackNotFound is Tickbuddy's 404 for a missing track.
func (s *Server) trackNotFound(w http.ResponseWriter) {
	data := map[string]string{"message": "Track not found"}
	if s.NotFoundReason != "" {
		data["reason"] = s.NotFoundReason
	}
	ocs(w, http.StatusNotFound, data)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	s.Queries = append(s.Queries, r.URL.RawQuery)
	var raw []byte
	if r.Body != nil {
		raw, _ = io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
	}
	s.Bodies = append(s.Bodies, string(raw))
	s.Prefers = append(s.Prefers, r.Header.Get("Prefer"))

	switch r.URL.Path {
	case "/index.php/login/v2":
		ocsPlain(w, http.StatusOK, map[string]any{
			"poll":  map[string]string{"token": "tok", "endpoint": s.URL + "/index.php/login/v2/poll"},
			"login": s.URL + "/index.php/login/v2/flow/abc",
		})
		return
	case "/index.php/login/v2/poll":
		_ = r.ParseForm()
		if r.PostForm.Get("token") != "tok" || s.LoginPolls > 0 {
			s.LoginPolls--
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ocsPlain(w, http.StatusOK, map[string]string{"server": s.URL, "loginName": s.User, "appPassword": s.LoginAppPassword})
		s.Password = s.LoginAppPassword
		return
	}

	if r.Header.Get("OCS-APIRequest") != "true" {
		msg(w, http.StatusBadRequest, "OCS-APIRequest header missing")
		return
	}
	if u, p, ok := r.BasicAuth(); !ok || u != s.User || p != s.Password || s.Revoked {
		ocs(w, http.StatusUnauthorized, []any{})
		return
	}
	if s.FailNext > 0 {
		s.FailNext--
		w.WriteHeader(s.Fail)
		_, _ = w.Write([]byte("<html>Bad gateway</html>"))
		return
	}

	switch path := r.URL.Path; {
	case path == "/ocs/v2.php/cloud/capabilities":
		caps := map[string]any{"core": map[string]any{"pollinterval": 60}}
		if !s.NoTickbuddy {
			caps["tickbuddy"] = map[string]any{"version": s.Version, "apiVersion": s.APIVersion, "features": s.Features}
		}
		ocs(w, http.StatusOK, map[string]any{"version": map[string]any{"string": "31.0.0"}, "capabilities": caps})
	case path == "/ocs/v2.php/core/apppassword" && r.Method == http.MethodDelete:
		s.Revoked = true
		ocs(w, http.StatusOK, []any{})
	case strings.HasPrefix(path, "/ocs/v2.php/apps/tickbuddy/api/") && s.Disabled:
		invalidQuery(w)
	case strings.HasPrefix(path, "/ocs/v2.php/apps/tickbuddy/api/"):
		s.api(w, r, strings.TrimPrefix(path, "/ocs/v2.php/apps/tickbuddy/api"))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func ocsPlain(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) track(id int) *Track {
	for i := range s.Tracks {
		if s.Tracks[i].ID == id {
			return &s.Tracks[i]
		}
	}
	return nil
}

// knownType reports whether a track type is one every client knows.
func knownType(typ string) bool { return typ == "boolean" || typ == "counter" }

// hidden reports whether a list leaves the track out: one of another type
// than yes/no or counter, unless the client asked for all types.
func (s *Server) hidden(id int, q url.Values) bool {
	t := s.track(id)
	return t != nil && !knownType(t.Type) && q.Get("includeAllTypes") != "true"
}

func trackJSON(t Track, order int) map[string]any {
	var archived any
	if t.ArchivedAt != "" {
		archived = t.ArchivedAt
	}
	out := map[string]any{
		"id": t.ID, "name": t.Name, "type": t.Type, "sortOrder": order, "private": t.Private, "archivedAt": archived,
		"unit": nil, "decimalPlaces": nil, "allowNegative": nil, "options": nil, "orderedScale": nil,
	}
	switch t.Type {
	case "number":
		out["unit"], out["decimalPlaces"], out["allowNegative"] = t.Unit, t.DecimalPlaces, t.AllowNegative
	case "choice":
		opts := []any{}
		for i, l := range t.Options {
			opts = append(opts, map[string]any{"value": i + 1, "label": l})
		}
		out["options"], out["orderedScale"] = opts, t.OrderedScale
	}
	return out
}

// wire is a stored value as the server writes it for t: a Number's
// thousandths as its exact decimal, any other value as it is.
func wire(t *Track, v int) any {
	if t == nil || t.Type != "number" {
		return v
	}
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	text := fmt.Sprintf("%s%d.%03d", sign, v/1000, v%1000)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "-0" {
		text = "0"
	}
	return json.Number(text)
}

// numberPattern is a Number as the server reads it, once trimmed.
var numberPattern = regexp.MustCompile(`^(-?)([0-9]+)(?:\.([0-9]+))?$`)

// parseNumber reads a Number sent to set (a JSON number or string) as
// thousandths rounded half away from zero to places, or an error message.
func parseNumber(raw json.RawMessage, places int) (int, string) {
	const notANumber = "Number tracks take a number, such as 10.4"
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, `"`) {
		var str string
		if json.Unmarshal(raw, &str) != nil {
			return 0, notANumber
		}
		text = strings.Trim(str, " \t\n\r\x00\x0b")
	}
	m := numberPattern.FindStringSubmatch(text)
	if m == nil {
		return 0, notANumber
	}
	whole := strings.TrimLeft(m[2], "0")
	if len(whole) > 7 {
		return 0, "Numbers must be between -2147483.647 and 2147483.647"
	}
	w, _ := strconv.Atoi("0" + whole)
	frac := m[3] + strings.Repeat("0", places+1)
	scale := 1
	for range 3 - places {
		scale *= 10
	}
	kept, _ := strconv.Atoi("0" + frac[:places])
	milli := w*1000 + kept*scale
	if frac[places] >= '5' {
		milli += scale
	}
	if milli > 2147483647 {
		return 0, "Numbers must be between -2147483.647 and 2147483.647"
	}
	if m[1] == "-" {
		milli = -milli
	}
	return milli, ""
}

// wrongType is the server's 400 for a call that doesn't fit the track's type.
func wrongType(w http.ResponseWriter, m string) {
	ocs(w, http.StatusBadRequest, map[string]string{"message": m, "reason": "wrongTrackType"})
}

func validDate(d string) bool {
	_, err := time.Parse("2006-01-02", d)
	return err == nil
}

func normalise(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Trim(text, " \t\n\r\x00\x0b")
}

// body is a write's JSON body; Note is nil when not sent.
type body struct {
	TrackID int             `json:"trackId"`
	Date    string          `json:"date"`
	Value   json.RawMessage `json:"value"`
	Delta   int             `json:"delta"`
	Note    *string         `json:"note"`
	// AppendNote and AllowDuplicateNote go with tick calls, AllowDuplicate
	// with POST /notes/append.
	AppendNote         *string `json:"appendNote"`
	AllowDuplicateNote bool    `json:"allowDuplicateNote"`
	AllowDuplicate     bool    `json:"allowDuplicate"`
	Text               *string `json:"text"`
	Name               *string `json:"name"`
	Type               string  `json:"type"`
	// Create's settings, and an option's new label.
	Unit          *string  `json:"unit"`
	DecimalPlaces *int     `json:"decimalPlaces"`
	AllowNegative *bool    `json:"allowNegative"`
	Options       []string `json:"options"`
	OrderedScale  *bool    `json:"orderedScale"`
	Label         *string  `json:"label"`
}

func (s *Server) api(w http.ResponseWriter, r *http.Request, path string) {
	var b body
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodDelete {
		_ = json.NewDecoder(r.Body).Decode(&b)
	}
	q := r.URL.Query()
	feat := s.Features
	isWrite := r.Method != http.MethodGet
	strict := strictHandling(r)

	answer := func(status int, data any) {
		if isWrite && s.ApplyThenFail > 0 {
			s.ApplyThenFail--
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		ocs(w, status, data)
	}

	switch {
	case path == "/tracks" && r.Method == http.MethodGet:
		out := []any{}
		for i, t := range s.Tracks {
			if t.ArchivedAt != "" && (q.Get("includeArchived") != "true" || !feat["archivedTracks"]) || s.hidden(t.ID, q) {
				continue
			}
			out = append(out, trackJSON(t, i))
		}
		ocs(w, http.StatusOK, out)

	case path == "/tracks" && r.Method == http.MethodPost:
		t := Track{ID: len(s.Tracks) + 1, Name: deref(b.Name), Type: b.Type}
		numberSettings := b.Unit != nil || b.DecimalPlaces != nil || b.AllowNegative != nil
		choiceSettings := b.Options != nil || b.OrderedScale != nil
		switch {
		case b.Type == "boolean" || b.Type == "counter":
			if numberSettings || choiceSettings {
				msg(w, http.StatusBadRequest, "These settings are not for this type of track")
				return
			}
		case b.Type == "number" && feat["numberTracks"]:
			if choiceSettings {
				msg(w, http.StatusBadRequest, "Options and ordered scale are only for choice tracks")
				return
			}
			t.Unit, t.DecimalPlaces = deref(b.Unit), 1
			if b.DecimalPlaces != nil {
				if *b.DecimalPlaces < 0 || *b.DecimalPlaces > 3 {
					msg(w, http.StatusBadRequest, "Decimal places must be from 0 to 3")
					return
				}
				t.DecimalPlaces = *b.DecimalPlaces
			}
			t.AllowNegative = b.AllowNegative != nil && *b.AllowNegative
		case b.Type == "choice" && feat["choiceTracks"]:
			if numberSettings {
				msg(w, http.StatusBadRequest, "Unit, decimal places and negative values are only for number tracks")
				return
			}
			if len(b.Options) < 2 || len(b.Options) > 99 {
				msg(w, http.StatusBadRequest, "A choice track needs 2 to 99 options")
				return
			}
			seen := map[string]bool{}
			for _, l := range b.Options {
				l = strings.TrimSpace(l)
				if l == "" || seen[strings.ToLower(l)] {
					msg(w, http.StatusBadRequest, "Each option needs a label, and no two alike")
					return
				}
				seen[strings.ToLower(l)] = true
				t.Options = append(t.Options, l)
			}
			t.OrderedScale = b.OrderedScale != nil && *b.OrderedScale
		default:
			msg(w, http.StatusBadRequest, "Type must be boolean or counter")
			return
		}
		s.Tracks = append(s.Tracks, t)
		answer(http.StatusCreated, trackJSON(t, t.ID-1))

	case strings.HasPrefix(path, "/tracks/") && strings.Contains(path, "/options/") && r.Method == http.MethodPut:
		if !feat["choiceTracks"] {
			invalidQuery(w)
			return
		}
		parts := strings.Split(strings.TrimPrefix(path, "/tracks/"), "/")
		id, _ := strconv.Atoi(parts[0])
		place, _ := strconv.Atoi(parts[len(parts)-1])
		t := s.track(id)
		switch {
		case t == nil:
			s.trackNotFound(w)
		case t.Type != "choice":
			wrongType(w, "Only choice tracks have options")
		case place < 1 || place > len(t.Options):
			ocs(w, http.StatusNotFound, map[string]string{"message": "Option not found", "reason": "optionNotFound"})
		case strings.TrimSpace(deref(b.Label)) == "":
			msg(w, http.StatusBadRequest, "Labels must be 1 to 255 characters")
		case t.ArchivedAt != "" || t.ReadOnly != "":
			reason := t.ReadOnly
			if reason == "" {
				reason = "archived"
			}
			ocs(w, http.StatusLocked, map[string]string{"message": fmt.Sprintf("%s is %s, so nothing was changed", t.Name, reason), "reason": reason})
		default:
			t.Options[place-1] = strings.TrimSpace(*b.Label)
			answer(http.StatusOK, trackJSON(*t, 0))
		}

	case strings.HasPrefix(path, "/tracks/") && r.Method == http.MethodPut:
		id, _ := strconv.Atoi(strings.TrimPrefix(path, "/tracks/"))
		t := s.track(id)
		if t == nil {
			s.trackNotFound(w)
			return
		}
		if locked(w, t, strict) {
			return
		}
		if b.Name != nil && t.ReadOnly == "" {
			t.Name = *b.Name
		}
		answer(http.StatusOK, trackJSON(*t, 0))

	case path == "/ticks" && r.Method == http.MethodGet:
		from, to := q.Get("from"), q.Get("to")
		out := []any{}
		n := 0
		add := func(k key, v any) {
			if k.date >= from && k.date <= to && !s.hidden(k.track, q) {
				n++
				out = append(out, map[string]any{"id": n, "trackId": k.track, "date": k.date, "value": v})
			}
		}
		for k, v := range s.Ticks {
			add(k, wire(s.track(k.track), v))
		}
		for k, v := range s.Values {
			add(k, v)
		}
		ocs(w, http.StatusOK, out)

	case path == "/ticks/toggle":
		if t := s.track(b.TrackID); t != nil && t.Type != "boolean" {
			wrongType(w, "Toggle is only for yes/no tracks")
			return
		}
		t, ok := s.writeChecks(w, b, strict, "boolean")
		if !ok {
			return
		}
		k := key{t.ID, b.Date}
		if !s.noteAllowed(w, b, k) {
			return
		}
		if t.writable() {
			if s.Ticks[k] > 0 {
				delete(s.Ticks, k)
			} else {
				s.Ticks[k] = 1
			}
		}
		out := map[string]any{"ticked": s.Ticks[k] > 0}
		if !s.writeNote(w, t, k, b, out) {
			return
		}
		answer(http.StatusOK, out)

	case path == "/ticks/set":
		if b.TrackID != 0 {
			if t := s.track(b.TrackID); t != nil && t.Type == "boolean" && !feat["booleanSet"] {
				msg(w, http.StatusBadRequest, "Set is only for counter tracks")
				return
			}
		}
		t, ok := s.writeChecks(w, b, strict, "")
		if !ok {
			return
		}
		k := key{t.ID, b.Date}
		var raw any
		_ = json.Unmarshal(b.Value, &raw)
		if t.Type == "number" || t.Type == "choice" {
			s.setValue(w, t, k, b, answer)
			return
		}
		if t.Type == "boolean" {
			want, ok := raw.(bool)
			if !ok {
				msg(w, http.StatusBadRequest, "Value must be true or false")
				return
			}
			if !s.noteAllowed(w, b, k) {
				return
			}
			if t.writable() {
				if want {
					s.Ticks[k] = 1
				} else {
					delete(s.Ticks, k)
				}
			}
			var v any = 0
			if s.Ticks[k] > 0 {
				v = 1
			}
			if s.BoolSetAnswer {
				v = s.Ticks[k] > 0
			}
			out := map[string]any{"value": v}
			if !s.writeNote(w, t, k, b, out) {
				return
			}
			answer(http.StatusOK, out)
			return
		}
		n, ok := raw.(float64)
		if !ok || n < 0 {
			msg(w, http.StatusBadRequest, "Value must be a whole number, 0 or more")
			return
		}
		if !s.noteAllowed(w, b, k) {
			return
		}
		if t.writable() {
			if n == 0 {
				delete(s.Ticks, k)
			} else {
				s.Ticks[k] = int(n)
			}
		}
		out := map[string]any{"value": s.Ticks[k]}
		if !s.writeNote(w, t, k, b, out) {
			return
		}
		answer(http.StatusOK, out)

	case path == "/ticks/increment":
		if !feat["counterIncrement"] {
			invalidQuery(w)
			return
		}
		if t := s.track(b.TrackID); t != nil && t.Type != "counter" {
			wrongType(w, "Increment is only for counter tracks")
			return
		}
		t, ok := s.writeChecks(w, b, strict, "counter")
		if !ok {
			return
		}
		if b.Delta == 0 {
			msg(w, http.StatusBadRequest, "Delta must not be 0")
			return
		}
		k := key{t.ID, b.Date}
		if !s.noteAllowed(w, b, k) {
			return
		}
		if t.writable() {
			if v := s.Ticks[k] + b.Delta; v > 0 {
				s.Ticks[k] = v
			} else {
				delete(s.Ticks, k)
			}
		}
		out := map[string]any{"value": s.Ticks[k]}
		if !s.writeNote(w, t, k, b, out) {
			return
		}
		answer(http.StatusOK, out)

	case path == "/stats":
		if !feat["stats"] {
			invalidQuery(w)
			return
		}
		s.StatsQueries = append(s.StatsQueries, q)
		from, to := q.Get("from"), q.Get("to")
		if (from != "" && !validDate(from)) || (to != "" && !validDate(to)) {
			msg(w, http.StatusBadRequest, "From and to must be dates as YYYY-MM-DD")
			return
		}
		if from != "" && to != "" && from > to {
			msg(w, http.StatusBadRequest, "From must be on or before to")
			return
		}
		out := []any{}
		id, _ := strconv.Atoi(q.Get("trackId"))
		for _, t := range s.Tracks {
			if id != 0 && t.ID != id {
				continue
			}
			if id == 0 && (t.ArchivedAt != "" && q.Get("includeArchived") != "true" || s.hidden(t.ID, q)) {
				continue
			}
			st := map[string]any{
				"trackId": t.ID, "total": 0, "weeklyMean": 0, "last7": 0, "previous7": 0, "peak7": 0,
				"current": nil, "upToYesterday": false, "longestStreak": nil, "longestBreak": nil,
				"firstTick": nil, "lastTick": nil, "maxValue": 0, "minValue": 0,
			}
			if t.Type == "number" || t.Type == "choice" {
				s.entryStats(&t, st)
			}
			for k, v := range s.Stats[t.ID] {
				st[k] = v
			}
			out = append(out, st)
		}
		if id != 0 && len(out) == 0 {
			s.trackNotFound(w)
			return
		}
		ocs(w, http.StatusOK, out)

	case path == "/notes" && r.Method == http.MethodGet:
		if !feat["notes"] {
			invalidQuery(w)
			return
		}
		from, to := q.Get("from"), q.Get("to")
		if !validDate(from) || !validDate(to) {
			msg(w, http.StatusBadRequest, "From and to must be dates as YYYY-MM-DD")
			return
		}
		type n struct {
			TrackID int    `json:"trackId"`
			Date    string `json:"date"`
			Text    string `json:"text"`
		}
		out := []n{}
		for k, v := range s.Notes {
			if k.date >= from && k.date <= to && !s.hidden(k.track, q) {
				out = append(out, n{k.track, k.date, v})
			}
		}
		slices.SortFunc(out, func(a, b n) int {
			if c := strings.Compare(b.Date, a.Date); c != 0 {
				return c
			}
			return a.TrackID - b.TrackID
		})
		ocs(w, http.StatusOK, out)

	case path == "/notes/set":
		if !feat["notes"] {
			invalidQuery(w)
			return
		}
		t, ok := s.writeChecks(w, b, strict, "")
		if !ok {
			return
		}
		text := normalise(deref(b.Text))
		if m := s.maxNote(); len([]rune(text)) > m {
			msg(w, http.StatusBadRequest, fmt.Sprintf("Notes can be at most %d characters", m))
			return
		}
		k := key{t.ID, b.Date}
		if t.writable() {
			if text == "" {
				delete(s.Notes, k)
			} else {
				s.Notes[k] = text
			}
		}
		answer(http.StatusOK, map[string]string{"text": s.Notes[k]})

	case path == "/notes/append":
		if !feat["notesAppend"] {
			invalidQuery(w)
			return
		}
		t, ok := s.writeChecks(w, b, strict, "")
		if !ok {
			return
		}
		k := key{t.ID, b.Date}
		text := normalise(deref(b.Text))
		if t.writable() {
			next, err := appended(s.Notes[k], text, b.AllowDuplicate)
			if err != "" {
				msg(w, http.StatusBadRequest, err)
				return
			}
			if s.ConflictNext > 0 {
				s.ConflictNext--
				msg(w, http.StatusConflict, "The note changed while appending; try again")
				return
			}
			if next != "" {
				s.Notes[k] = next
			}
		}
		answer(http.StatusOK, map[string]string{"text": s.Notes[k]})

	case path == "/notes" && r.Method == http.MethodDelete:
		if !feat["notes"] {
			invalidQuery(w)
			return
		}
		id, _ := strconv.Atoi(q.Get("trackId"))
		t := s.track(id)
		if t == nil {
			s.trackNotFound(w)
			return
		}
		if locked(w, t, strict) {
			return
		}
		if t.writable() {
			delete(s.Notes, key{id, q.Get("date")})
		}
		answer(http.StatusNoContent, nil)

	case path == "/export":
		tracks := []any{}
		for _, t := range s.Tracks {
			if t.Private && q.Get("includePrivate") != "true" {
				continue
			}
			tracks = append(tracks, map[string]any{"name": t.Name, "type": t.Type})
		}
		ocs(w, http.StatusOK, map[string]any{"version": 1, "exportedAt": "2026-10-18T10:00:00+00:00", "tracks": tracks, "ticks": []any{}})

	default:
		invalidQuery(w)
	}
}

// setValue answers set on a Number or Choice track: null or "" clears the
// day, a missing value is 400, a Number is rounded and checked, a Choice's
// place must be one of its options.
func (s *Server) setValue(w http.ResponseWriter, t *Track, k key, b body, answer func(int, any)) {
	text := strings.TrimSpace(string(b.Value))
	if b.Value == nil {
		if t.Type == "number" {
			msg(w, http.StatusBadRequest, "Number tracks take a number, such as 10.4")
		} else {
			msg(w, http.StatusBadRequest, fmt.Sprintf("Choice tracks take the number of an option, from 1 to %d", len(t.Options)))
		}
		return
	}
	clear := text == "null" || text == `""`
	var v int
	if !clear && t.Type == "number" {
		var m string
		if v, m = parseNumber(b.Value, t.DecimalPlaces); m != "" {
			msg(w, http.StatusBadRequest, m)
			return
		}
		if v < 0 && !t.AllowNegative {
			msg(w, http.StatusBadRequest, "This track doesn't allow negative values")
			return
		}
	}
	if !clear && t.Type == "choice" {
		var err error
		if v, err = strconv.Atoi(strings.Trim(text, `"`)); err != nil || v < 1 || v > len(t.Options) {
			msg(w, http.StatusBadRequest, fmt.Sprintf("Choice tracks take the number of an option, from 1 to %d", len(t.Options)))
			return
		}
	}
	if !s.noteAllowed(w, b, k) {
		return
	}
	if t.writable() {
		if clear {
			delete(s.Ticks, k)
		} else {
			s.Ticks[k] = v
		}
	}
	out := map[string]any{"value": nil}
	if stored, ok := s.Ticks[k]; ok {
		out["value"] = wire(t, stored)
	}
	if !s.writeNote(w, t, k, b, out) {
		return
	}
	answer(http.StatusOK, out)
}

// entryStats fills a Number or Choice track's stats as the server works
// them out for now: total counts the days with an entry; maxValue and
// minValue are the highest and lowest entry (a Number), place (an ordered
// scale) or null (a plain list, or no entry). Canned Stats still win.
func (s *Server) entryStats(t *Track, st map[string]any) {
	var vals []int
	for k, v := range s.Ticks {
		if k.track == t.ID {
			vals = append(vals, v)
		}
	}
	st["total"] = len(vals)
	st["maxValue"], st["minValue"] = nil, nil
	if len(vals) > 0 && (t.Type == "number" || t.OrderedScale) {
		st["maxValue"], st["minValue"] = wire(t, slices.Max(vals)), wire(t, slices.Min(vals))
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// writeChecks runs the server's checks in its order: track (404), type
// (400), date (400), read-only (423).
func (s *Server) writeChecks(w http.ResponseWriter, b body, strict bool, typ string) (*Track, bool) {
	t := s.track(b.TrackID)
	if t == nil {
		s.trackNotFound(w)
		return nil, false
	}
	if typ != "" && t.Type != typ {
		msg(w, http.StatusBadRequest, fmt.Sprintf("This only works on %s tracks", typ))
		return nil, false
	}
	if !validDate(b.Date) {
		msg(w, http.StatusBadRequest, "Date must be a real date as YYYY-MM-DD")
		return nil, false
	}
	if locked(w, t, strict) {
		return nil, false
	}
	return t, true
}

// strictHandling reports whether the request asks for Prefer:
// handling=strict (RFC 7240).
func strictHandling(r *http.Request) bool {
	for _, v := range r.Header.Values("Prefer") {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), "handling=strict") {
				return true
			}
		}
	}
	return false
}

// locked answers 423 for a read-only track when the client asked for strict
// handling, and reports whether it did.
func locked(w http.ResponseWriter, t *Track, strict bool) bool {
	if t.ReadOnly == "" || !strict {
		return false
	}
	m := fmt.Sprintf("%s is %s, so nothing was changed", t.Name, t.ReadOnly)
	ocs(w, http.StatusLocked, map[string]string{"message": m, "reason": t.ReadOnly})
	return true
}

// maxNote is the longest note the server stores.
func (s *Server) maxNote() int {
	if s.Features["notesAppend"] {
		return 500
	}
	return 200
}

// appended is note with text added on a new line, "" when text adds
// nothing (empty, or its lines already there), or an error message when the
// result would be too long.
func appended(note, text string, allowDuplicate bool) (string, string) {
	switch {
	case text == "":
		return "", ""
	case note == "":
		return text, ""
	case !allowDuplicate && containsLines(note, text):
		return "", ""
	}
	out := note + "\n" + text
	if len([]rune(out)) > 500 {
		return "", "The note would be longer than 500 characters"
	}
	return out, ""
}

func containsLines(note, text string) bool {
	lines, want := strings.Split(note, "\n"), strings.Split(text, "\n")
	for i := 0; i+len(want) <= len(lines); i++ {
		if slices.Equal(lines[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

// noteAllowed checks a tick call's note before anything is written; an
// older server ignores note (and appendNote) silently.
func (s *Server) noteAllowed(w http.ResponseWriter, b body, k key) bool {
	if !s.Features["notes"] {
		return true
	}
	appendNote := b.AppendNote
	if !s.Features["notesAppend"] {
		appendNote = nil
	}
	if b.Note != nil && appendNote != nil {
		msg(w, http.StatusBadRequest, "Send note or appendNote, not both")
		return false
	}
	if b.Note != nil {
		if m := s.maxNote(); len([]rune(normalise(*b.Note))) > m {
			msg(w, http.StatusBadRequest, fmt.Sprintf("Notes can be at most %d characters", m))
			return false
		}
	}
	if appendNote != nil {
		if _, err := appended(s.Notes[k], normalise(*appendNote), b.AllowDuplicateNote); err != "" {
			msg(w, http.StatusBadRequest, err)
			return false
		}
	}
	return true
}

// writeNote writes a tick call's note after its tick and adds it to out.
// It answers a forced 409 itself and returns false then.
func (s *Server) writeNote(w http.ResponseWriter, t *Track, k key, b body, out map[string]any) bool {
	if !s.Features["notes"] {
		return true
	}
	if b.AppendNote != nil && s.Features["notesAppend"] {
		if t.writable() {
			if s.ConflictNext > 0 {
				s.ConflictNext--
				msg(w, http.StatusConflict, "The note changed while appending; try again")
				return false
			}
			if next, _ := appended(s.Notes[k], normalise(*b.AppendNote), b.AllowDuplicateNote); next != "" {
				s.Notes[k] = next
			}
		}
		out["note"] = s.Notes[k]
		return true
	}
	if b.Note == nil {
		return true
	}
	if t.writable() {
		if text := normalise(*b.Note); text == "" {
			delete(s.Notes, k)
		} else {
			s.Notes[k] = text
		}
	}
	out["note"] = s.Notes[k]
	return true
}
