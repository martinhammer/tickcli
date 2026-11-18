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
	if id, ok := hashNumber(query); ok {
		for _, t := range tracks {
			if t.ID == id {
				return t, nil
			}
		}
		return Track{}, &MatchError{Kind: ErrTrackNotFound, Query: query}
	}
	m := match(tracks, func(t Track) string { return t.Name }, query, interactive)
	switch {
	case m.found:
		return m.one, nil
	case m.ambiguous != nil:
		return Track{}, &MatchError{Kind: ErrAmbiguous, Query: query, Candidates: m.ambiguous}
	}
	return Track{}, &MatchError{Kind: ErrTrackNotFound, Query: query, Candidates: m.similar}
}

// ResolveOption finds the option of the Choice track t that query names, as
// Resolve finds a track:
//
//   - "#2" is the option in place 2 (a label that itself starts with "#"
//     and a digit is reached by its place);
//   - otherwise a label exactly, ignoring case and surrounding spaces, so a
//     bare "2" is a label, never a place;
//   - only when interactive, a unique prefix of a label.
//
// No match is an *OptionError for ErrOptionNotFound listing every option,
// several are one for ErrOptionAmbiguous listing those. Two labels the server
// tells apart but this comparison doesn't (a corner of Unicode case folding)
// are ambiguous, and "#N" reaches either.
func ResolveOption(t Track, query string, interactive bool) (ChoiceOption, error) {
	if place, ok := hashNumber(query); ok {
		if o, ok := t.Option(place); ok {
			return o, nil
		}
		return ChoiceOption{}, &OptionError{Kind: ErrOptionNotFound, Track: t.Name, Query: query, Candidates: t.Options}
	}
	m := match(t.Options, func(o ChoiceOption) string { return o.Label }, query, interactive)
	switch {
	case m.found:
		return m.one, nil
	case m.ambiguous != nil:
		return ChoiceOption{}, &OptionError{Kind: ErrOptionAmbiguous, Track: t.Name, Query: query, Candidates: m.ambiguous}
	}
	return ChoiceOption{}, &OptionError{Kind: ErrOptionNotFound, Track: t.Name, Query: query, Candidates: t.Options}
}

// hashNumber reads "#12", ignoring surrounding spaces.
func hashNumber(query string) (int, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(query), "#")
	if !ok || !allDigits(rest) {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// matched is what match found: one item, several (ambiguous), or neither,
// with the items that look similar.
type matched[T any] struct {
	one       T
	found     bool
	ambiguous []T
	similar   []T
}

// match is the name matching of Resolve and ResolveOption.
func match[T any](items []T, name func(T) string, query string, interactive bool) matched[T] {
	fold := strings.ToLower(strings.TrimSpace(query))
	var exact, prefix, similar []T
	for _, it := range items {
		n := strings.ToLower(strings.TrimSpace(name(it)))
		switch {
		case n == fold:
			exact = append(exact, it)
		case strings.HasPrefix(n, fold):
			prefix = append(prefix, it)
		case fold != "" && strings.Contains(n, fold):
			similar = append(similar, it)
		}
	}
	switch {
	case len(exact) == 1:
		return matched[T]{one: exact[0], found: true}
	case len(exact) > 1:
		return matched[T]{ambiguous: exact}
	case interactive && fold != "" && len(prefix) == 1:
		return matched[T]{one: prefix[0], found: true}
	case interactive && fold != "" && len(prefix) > 1:
		return matched[T]{ambiguous: prefix}
	}
	return matched[T]{similar: append(prefix, similar...)}
}
