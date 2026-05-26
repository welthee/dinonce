package psql

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// pgx's stdlib driver does not transparently encode/decode PostgreSQL arrays
// through the database/sql interface — arrays surface as the text literal
// "{a,b,c}" instead of as a Go slice. Rather than reach for the underlying
// pgx.Conn we wrap the parameters with these tiny Valuer/Scanner pairs.
// They speak only the Postgres array text format that every Postgres driver
// understands, so the code stays driver-agnostic.

// stringArray sends/receives a Postgres character varying(64)[] as []string.
type stringArray []string

func (a stringArray) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil
	}
	var b strings.Builder
	b.WriteByte('{')
	for i, s := range a {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		// Escape backslashes and double quotes per Postgres array literal rules.
		for j := 0; j < len(s); j++ {
			switch s[j] {
			case '\\', '"':
				b.WriteByte('\\')
			}
			b.WriteByte(s[j])
		}
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String(), nil
}

// int64Array sends/receives a Postgres bigint[] as []int64.
type int64Array []int64

func (a *int64Array) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	var s string
	switch v := src.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("psql: cannot scan %T into int64Array", src)
	}
	if len(s) < 2 || s[0] != '{' || s[len(s)-1] != '}' {
		return fmt.Errorf("psql: %q is not a Postgres array literal", s)
	}
	inner := s[1 : len(s)-1]
	if inner == "" {
		*a = nil
		return nil
	}
	parts := strings.Split(inner, ",")
	out := make([]int64, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return fmt.Errorf("psql: parse %q in bigint[]: %w", p, err)
		}
		out = append(out, n)
	}
	*a = out
	return nil
}
