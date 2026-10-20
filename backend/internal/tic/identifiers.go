package tic

import (
	"errors"
	"fmt"
	"strings"
)

// Provider identifier normalization.
//
// Real TiC files are inconsistent about identifier formatting: EINs appear with
// and without dashes, and NPIs appear as JSON numbers, as strings, and
// occasionally as placeholders. Search has to work regardless of how a user
// types the identifier, so every identifier is reduced to a canonical
// digits-only form before it is indexed or compared.
//
// Measured against the two Fidelis in-network files: of 43,834 distinct NPIs in
// fidelis-ex, 43,832 pass the official check-digit test. The two that fail are
// the placeholder `0` and the nine-digit value `124507857`, which lost a
// leading zero to JSON number typing. Those records are real data a user may
// legitimately need to find, which is why structural problems are reported as
// warnings and never block a lookup.

// ErrNoDigits means the input contained no digits at all, so there is nothing
// to search for. This is the only identifier condition treated as an error.
var ErrNoDigits = errors.New("no digits found in identifier")

// Identifier is a normalized provider identifier plus everything we noticed
// about it on the way. Warnings are surfaced to the user rather than enforced.
type Identifier struct {
	// Raw is the input exactly as supplied.
	Raw string `json:"raw"`
	// Normalized is the digits-only form used for index lookups.
	Normalized string `json:"normalized"`
	// Display is the conventionally formatted form.
	Display string `json:"display"`
	// Valid reports whether the identifier passed every structural check.
	Valid bool `json:"valid"`
	// Warnings explains each failed check in user-facing terms.
	Warnings []string `json:"warnings,omitempty"`
}

func digitsOnly(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseNPI normalizes a National Provider Identifier.
//
// The returned Identifier is always usable for a lookup when err is nil, even
// when Valid is false: a malformed NPI present in the source data must remain
// findable.
func ParseNPI(s string) (Identifier, error) {
	id := Identifier{Raw: s}
	d := digitsOnly(s)
	if d == "" {
		return id, ErrNoDigits
	}
	id.Normalized = d
	id.Display = d
	id.Valid = true

	if len(d) != 10 {
		id.Valid = false
		id.Warnings = append(id.Warnings,
			fmt.Sprintf("an NPI is 10 digits; this is %d", len(d)))
	} else if !npiChecksumOK(d) {
		id.Valid = false
		id.Warnings = append(id.Warnings, "fails the NPI check-digit test")
	}
	return id, nil
}

// npiChecksumOK implements the CMS NPI check-digit algorithm: the Luhn
// algorithm applied to the nine-digit identifier prefixed with the constant
// "80840", the ISO 7812 issuer identifier assigned to CMS.
//
// Verified against the CMS published example 1234567893.
func npiChecksumOK(npi string) bool {
	if len(npi) != 10 {
		return false
	}
	payload := "80840" + npi[:9]
	check := int(npi[9] - '0')

	sum := 0
	for i := 0; i < len(payload); i++ {
		d := int(payload[len(payload)-1-i] - '0')
		if i%2 == 0 { // double every second digit, counting from the right
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (sum+check)%10 == 0
}

// ParseEIN normalizes an Employer Identification Number. Both "11-3471596" and
// "113471596" normalize to "113471596" and display as "11-3471596".
func ParseEIN(s string) (Identifier, error) {
	id := Identifier{Raw: s}
	d := digitsOnly(s)
	if d == "" {
		return id, ErrNoDigits
	}
	id.Normalized = d
	id.Display = FormatEIN(d)
	id.Valid = true

	if len(d) != 9 {
		id.Valid = false
		id.Warnings = append(id.Warnings,
			fmt.Sprintf("an EIN is 9 digits; this is %d", len(d)))
	}
	return id, nil
}

// FormatEIN renders a 9-digit EIN in the conventional NN-NNNNNNN form. Input
// that is not exactly 9 digits is returned unchanged, so display never implies
// a validity the value does not have.
func FormatEIN(digits string) string {
	if len(digits) != 9 {
		return digits
	}
	return digits[:2] + "-" + digits[2:]
}

// NPIKey normalizes an NPI as it appeared in a source file into the same
// digits-only key space used for lookups. The placeholder 0 is preserved so it
// stays visible as a data-quality signal rather than silently disappearing.
func NPIKey(v FlexInt64) string {
	return fmt.Sprintf("%d", int64(v))
}
