package components

import (
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

func itoa(i int64) string {
	return strconv.FormatInt(i, 10)
}

func itoaInt(i int) string {
	return strconv.Itoa(i)
}

func formatDate(t sql.NullTime) string {
	if t.Valid {
		return t.Time.Format(time.DateOnly)
	}
	return "—"
}

func formatString(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return "—"
}

func formatPrice(p sql.NullFloat64) string {
	if p.Valid {
		return fmt.Sprintf("$%.2f", p.Float64)
	}
	return "—"
}

func formatPriceValue(p sql.NullFloat64) string {
	if p.Valid {
		return fmt.Sprintf("%.2f", p.Float64)
	}
	return ""
}

func ParseDate(s string) sql.NullTime {
	if s == "" {
		return sql.NullTime{}
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}

func ParseOptionalInt(s string) sql.NullInt64 {
	if s == "" {
		return sql.NullInt64{}
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: i, Valid: true}
}

func ParseOptionalFloat(s string) sql.NullFloat64 {
	if s == "" {
		return sql.NullFloat64{}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}

func OptionalString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func StrconvID(id int64) string {
	return strconv.FormatInt(id, 10)
}
