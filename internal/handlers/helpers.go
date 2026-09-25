package handlers

import (
	"database/sql"
	"math"
	"time"
)

func parseDate(s string) sql.NullTime {
	if s == "" {
		return sql.NullTime{}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func optionalString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// toCents converts a decimal amount to whole cents, rounding to the nearest.
func toCents(amount float64) int64 {
	return int64(math.Round(amount * 100))
}
