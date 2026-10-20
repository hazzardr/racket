package handlers

import (
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// parseDate parses an optional YYYY-MM-DD date. ok is false only for a
// date that's there but isn't one.
func parseDate(s string) (sql.NullTime, bool) {
	if s == "" {
		return sql.NullTime{}, true
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return sql.NullTime{}, false
	}
	return sql.NullTime{Time: t, Valid: true}, true
}

func optionalString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Length limits for the text people type, in characters. PCPartPicker's
// longest product names run to about 150.
const (
	maxNameLen        = 100 // machine names
	maxCategoryLen    = 50  // slot and part categories
	maxPartNameLen    = 300
	maxPartFieldLen   = 100 // manufacturer, model, merchant
	maxLocationLen    = 200
	maxDescriptionLen = 2000
)

// textField is a form value with its label and length limit, for tooLong.
type textField struct {
	label string
	value string
	max   int
}

// tooLong returns a validation message for the first field longer than its
// limit, or "" if they all fit.
func tooLong(fields ...textField) string {
	for _, f := range fields {
		if utf8.RuneCountInString(f.value) > f.max {
			return fmt.Sprintf("%s can be at most %d characters.", f.label, f.max)
		}
	}
	return ""
}

// partNameRequired is the validation message for a part with no name.
const partNameRequired = "Part name is required."

// maxPriceCents caps prices at $1,000,000, so totals can't overflow.
const maxPriceCents = 100_000_000

// badPrice is the validation message for a price parsePrice rejects.
const badPrice = "Price should be a number, like 129.99, and at most 1000000."

var (
	// priceRe splits a price into an optional currency before the amount
	// (a code like "USD", a symbol, or both, like "US$"), the amount, and
	// an optional currency after it.
	priceRe = regexp.MustCompile(`^([A-Za-z]{0,3})\s*([$£€¥₹]?)\s*([0-9][0-9.,]*)\s*([$£€¥₹]?)\s*([A-Za-z]{0,3})$`)
	// commaDecimalRe is an amount written with a decimal comma, "12,50" or
	// "1.299,00": a comma with exactly two digits after it, at the end.
	commaDecimalRe = regexp.MustCompile(`^[0-9.]*[0-9],[0-9]{2}$`)
	// groupedRe is an amount's whole part with optional thousands
	// separators sep, "1,299" or "1299".
	groupedRe = map[string]*regexp.Regexp{
		",": regexp.MustCompile(`^[0-9]{1,3}(,[0-9]{3})+$|^[0-9]+$`),
		".": regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{3})+$|^[0-9]+$`),
	}
)

// currencyCodes are the codes a price may name its currency with, and
// currencyPrefixes the letters that may come right before a "$" ("US$").
var (
	currencyCodes = map[string]bool{
		"usd": true, "eur": true, "gbp": true, "cad": true, "aud": true, "nzd": true,
		"inr": true, "jpy": true, "chf": true, "sek": true, "nok": true, "dkk": true, "pln": true,
	}
	currencyPrefixes = map[string]bool{"us": true, "ca": true, "c": true, "a": true, "au": true, "nz": true}
)

// parsePrice parses a price like "$1,299.99", "€1.299,00", "12,50 EUR" or
// "£150" into whole cents, rounding to the nearest. A comma with exactly two
// digits after it is a decimal comma (and dots are thousands separators);
// any other comma separates thousands. It returns false for anything else:
// empty, negative, letters that aren't a currency, or over maxPriceCents.
func parsePrice(s string) (int64, bool) {
	m := priceRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	before, amount, after := strings.ToLower(m[1]), m[3], strings.ToLower(m[5])
	switch {
	case m[2] != "" && m[4] != "", before != "" && after != "":
		return 0, false
	case before != "" && !currencyCodes[before] && (m[2] != "$" || !currencyPrefixes[before]):
		return 0, false
	case after != "" && !currencyCodes[after]:
		return 0, false
	}

	whole, frac, sep := amount, "", ","
	if commaDecimalRe.MatchString(amount) {
		whole, frac, _ = strings.Cut(amount, ",")
		sep = "."
	} else if i := strings.LastIndexByte(amount, '.'); i >= 0 {
		whole, frac = amount[:i], amount[i+1:]
		if frac == "" || strings.ContainsAny(frac, ",.") {
			return 0, false
		}
	}
	if !groupedRe[sep].MatchString(whole) {
		return 0, false
	}
	number := strings.ReplaceAll(whole, sep, "")
	if frac != "" {
		number += "." + frac
	}
	v, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0, false
	}
	cents := math.Round(v * 100)
	if cents > maxPriceCents {
		return 0, false
	}
	return int64(cents), true
}

// formPrice parses the optional form field "price". ok is false for a
// price that's there but isn't one.
func formPrice(r *http.Request) (sql.NullInt64, bool) {
	s := strings.TrimSpace(r.FormValue("price"))
	if s == "" {
		return sql.NullInt64{}, true
	}
	cents, ok := parsePrice(s)
	return sql.NullInt64{Int64: cents, Valid: ok}, ok
}

// matchKey is a name or category as matching compares it, the same as the
// queries' lower(trim(name)): SQLite's trim strips spaces and its lower
// folds ASCII letters only.
func matchKey(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, strings.Trim(s, " "))
}
