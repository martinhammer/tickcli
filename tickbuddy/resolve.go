// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"strconv"
	"strings"
)

// Resolve finds the track query names among tracks:
//
//   - "#12" matches the track with id 12;
//   - otherwise an exact name, ignoring case and surrounding spaces;
//   - only when interactive (a person at a terminal), a unique prefix of a
//     name also matches, so scripts never hit a track by accident.
//
// Archived and private tracks take part like any other: an explicit name
// always works. No match is a *MatchError for ErrTrackNotFound, several are
// one for ErrAmbiguous; both list the candidates.
func Resolve(tracks []Track, query string, interactive bool) (Track, error) {
	q := strings.TrimSpace(query)
	if rest, ok := strings.CutPrefix(q, "#"); ok && allDigits(rest) {
		id, _ := strconv.Atoi(rest)
		for _, t := range tracks {
			if t.ID == id {
				return t, nil
			}
		}
		return Track{}, &MatchError{Kind: ErrTrackNotFound, Query: query}
	}

	fold := strings.ToLower(q)
	var exact, prefix, similar []Track
	for _, t := range tracks {
		name := strings.ToLower(strings.TrimSpace(t.Name))
		switch {
		case name == fold:
			exact = append(exact, t)
		case strings.HasPrefix(name, fold):
			prefix = append(prefix, t)
		case fold != "" && strings.Contains(name, fold):
			similar = append(similar, t)
		}
	}
	switch {
	case len(exact) == 1:
		return exact[0], nil
	case len(exact) > 1:
		return Track{}, &MatchError{Kind: ErrAmbiguous, Query: query, Candidates: exact}
	case interactive && fold != "" && len(prefix) == 1:
		return prefix[0], nil
	case interactive && fold != "" && len(prefix) > 1:
		return Track{}, &MatchError{Kind: ErrAmbiguous, Query: query, Candidates: prefix}
	}
	return Track{}, &MatchError{Kind: ErrTrackNotFound, Query: query, Candidates: append(prefix, similar...)}
}
