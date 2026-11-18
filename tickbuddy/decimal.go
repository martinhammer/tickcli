// SPDX-License-Identifier: GPL-3.0-or-later

package tickbuddy

import (
	"errors"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Decimal is a Number track's value, held as whole thousandths, as the server
// stores it (10.4 is 10400). It is never a float: ParseDecimal reads the
// server's text exactly, and String and Format write it back exactly.
// Rounding a value sent to a track's decimal places is the server's.
type Decimal struct{ milli int64 }

// jsonNumber is a JSON number's text.
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?([0-9]{1,4}))?$`)

// ParseDecimal reads a JSON number as the server answers it (10.4, -1.5, 72,
// and 1e-3 should a server ever write an exponent). It must be a whole number
// of thousandths: 10.4567 is an error, never rounded.
func ParseDecimal(s string) (Decimal, error) {
	if !jsonNumber.MatchString(s) {
		return Decimal{}, errors.New("tickbuddy: not a number: " + strconv.Quote(s))
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return Decimal{}, errors.New("tickbuddy: not a number: " + strconv.Quote(s))
	}
	r.Mul(r, big.NewRat(1000, 1))
	// MinInt64 is left out so that every Decimal's magnitude fits an int64.
	if !r.IsInt() || !r.Num().IsInt64() || r.Num().Int64() == math.MinInt64 {
		return Decimal{}, errors.New("tickbuddy: not a number of thousandths: " + strconv.Quote(s))
	}
	return Decimal{r.Num().Int64()}, nil
}

// Milli is the value in thousandths.
func (d Decimal) Milli() int64 { return d.milli }

// String is the stored value in its shortest exact form, as the API writes
// it: "10.4", "-1.5", "72", "0", "10.45".
func (d Decimal) String() string {
	s := d.Format(3)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Format rounds the value half away from zero to places decimals (0 to 3)
// and writes exactly that many, with a point and no grouping: 10.45 at 1 is
// "10.5", 72 at 1 is "72.0", 10.4 at 0 is "10". A result of zero never
// carries a minus. This is how the web shows a day of a track with that many
// decimal places.
func (d Decimal) Format(places int) string {
	places = min(max(places, 0), 3)
	scale := int64(1)
	for range 3 - places {
		scale *= 10
	}
	mag := d.milli
	if mag < 0 {
		mag = -mag
	}
	q := mag / scale
	if r := mag % scale; scale > 1 && r >= scale/2 {
		q++
	}
	unit := int64(1)
	for range places {
		unit *= 10
	}
	s := strconv.FormatInt(q/unit, 10)
	if places > 0 {
		frac := strconv.FormatInt(q%unit, 10)
		s += "." + strings.Repeat("0", places-len(frac)) + frac
	}
	if d.milli < 0 && q != 0 {
		s = "-" + s
	}
	return s
}

// MarshalJSON writes String as a JSON number.
func (d Decimal) MarshalJSON() ([]byte, error) { return []byte(d.String()), nil }

// numberForm is a Number as the API takes it, once trimmed: a point, no
// grouping, no plus and no exponent.
var numberForm = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// ValidNumber reports whether s, trimmed, is a number as SetNumber sends it:
// 10.4, -1.5, 007.50. An empty string, or one of only spaces, is not one:
// clear a day with ClearValue instead.
func ValidNumber(s string) bool { return numberForm.MatchString(strings.TrimSpace(s)) }
