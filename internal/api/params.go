package api

import (
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

var decimalNumber = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

type numberSyntax struct {
	unicodeSpace bool
	goSyntax     bool
	wideRadix    bool
	radixFloat   bool
}

// numberValue keeps the established route-specific parsing differences explicit.
// Strict parsers reject Go underscores/hex floats; the space routes also accept
// radix integers wider than uint64. All conversions still use float64 values.
func numberValue(v string, syntax numberSyntax) float64 {
	if syntax.unicodeSpace {
		v = strings.TrimSpace(v)
	} else {
		v = Trim(v)
	}
	if v == "" {
		return 0
	}
	if len(v) > 2 && v[0] == '0' && strings.ContainsAny(v[1:2], "xXoObB") {
		if syntax.wideRadix {
			base := 16
			switch v[1] {
			case 'o', 'O':
				base = 8
			case 'b', 'B':
				base = 2
			}
			if !strings.ContainsAny(v[2:], "+-_") {
				if n, ok := new(big.Int).SetString(v[2:], base); ok {
					f, _ := new(big.Float).SetInt(n).Float64()
					return f
				}
			}
		} else if syntax.goSyntax || !strings.Contains(v, "_") {
			if n, err := strconv.ParseUint(v, 0, 64); err == nil {
				return float64(n)
			}
		}
		if !syntax.radixFloat {
			return math.NaN()
		}
	}
	if !syntax.goSyntax && !decimalNumber.MatchString(v) {
		return math.NaN()
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return math.NaN()
	}
	return n
}
func finiteNumber(v string, fallback float64, syntax numberSyntax) float64 {
	if v == "" {
		return fallback
	}
	n := numberValue(v, syntax)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return fallback
	}
	return n
}

// Number parses finite form numbers with ECMAScript whitespace and uint64 radix bounds.
func Number(v string, fallback float64) float64 { return finiteNumber(v, fallback, numberSyntax{}) }

// NumberUnicode preserves discovery/PGC's Go Unicode whitespace handling.
func NumberUnicode(v string, fallback float64) float64 {
	return finiteNumber(v, fallback, numberSyntax{unicodeSpace: true})
}

// NumberWide preserves the space routes' arbitrary-width radix parsing.
func NumberWide(v string, fallback float64) float64 {
	return finiteNumber(v, fallback, numberSyntax{wideRadix: true})
}

// NumberGo preserves creator routes' acceptance of Go numeric separators.
func NumberGo(v string, fallback float64) float64 {
	return finiteNumber(v, fallback, numberSyntax{goSyntax: true})
}

// NumberUnicodeGo preserves admin's Unicode whitespace and numeric separators.
func NumberUnicodeGo(v string, fallback ...float64) float64 {
	d := float64(0)
	if len(fallback) > 0 {
		d = fallback[0]
	}
	return finiteNumber(v, d, numberSyntax{unicodeSpace: true, goSyntax: true})
}

// NumberUnicodeGoFloat also accepts Go hexadecimal floats, as live routes did.
func NumberUnicodeGoFloat(v string, fallback float64) float64 {
	return finiteNumber(v, fallback, numberSyntax{unicodeSpace: true, goSyntax: true, radixFloat: true})
}

// RawNumber preserves NaN/Infinity for admin JSON coercions and settings.
func RawNumber(v string) float64 {
	return numberValue(v, numberSyntax{unicodeSpace: true, goSyntax: true})
}

// DBID binds NULL for non-integral or out-of-range IDs instead of letting pgx
// truncate them to a different record's integer primary key.
func DBID(n float64) any {
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < -0x1p63 || n >= 0x1p63 {
		return nil
	}
	return int64(n)
}
func idOr(n float64, invalid int64) int64 {
	if id, ok := DBID(n).(int64); ok {
		return id
	}
	return invalid
}
func NonnegativeID(n float64) int64 {
	if n < 0 {
		return 0
	}
	return idOr(n, 0)
}

// ParseWideID and ParseUnicodeID retain the zero sentinel of space/live routes.
func ParseWideID(v string) int64    { return idOr(NumberWide(v, 0), 0) }
func ParseUnicodeID(v string) int64 { return idOr(NumberUnicodeGoFloat(v, 0), 0) }

// ParseID retains the community routes' MinInt64 invalid-reference sentinel.
func ParseID(v string) int64 { return idOr(Number(v, 0), math.MinInt64) }
func ParseGoID(v string) any { return DBID(NumberGo(v, 0)) }

// Int is deliberately truncating; use a checked ID parser for references.
func Int(v string, fallback ...int64) int64 {
	d := int64(0)
	if len(fallback) > 0 {
		d = fallback[0]
	}
	return int64(Number(v, float64(d)))
}

// Slice matches JS Array.slice, including negative bounds and fractional indices.
func Slice[T any](rows []T, start, end float64) []T {
	index := func(n float64) int {
		if math.IsNaN(n) {
			return 0
		}
		n = math.Trunc(n)
		if n < 0 {
			n = math.Max(float64(len(rows))+n, 0)
		}
		return int(math.Min(n, float64(len(rows))))
	}
	a, b := index(start), index(end)
	if b < a {
		b = a
	}
	return rows[a:b]
}
func Page[T any](rows []T, pn, ps float64) []T {
	pn = math.Max(1, pn)
	return Slice(rows, (pn-1)*ps, pn*ps)
}

// PageResult echoes the unnormalized request page and returns a non-nil copy.
func PageResult[T any](rows []T, c *Ctx, defaultSize float64) ([]T, map[string]any) {
	pn, ps := NumberUnicodeGo(c.P["pn"], 1), NumberUnicodeGo(c.P["ps"], defaultSize)
	return append([]T{}, Page(rows, pn, ps)...), map[string]any{"pn": pn, "ps": ps, "total": len(rows)}
}

// Trim uses ECMAScript WhiteSpace/LineTerminator, which differs from TrimSpace.
func Trim(v string) string {
	return strings.Trim(v, "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
}
func UTF16Length(v string) int { return len(utf16.Encode([]rune(v))) }

// UTF16Prefix replaces a cut surrogate with U+FFFD so SQL receives valid Unicode.
func UTF16Prefix(v string, n int) string {
	units := utf16.Encode([]rune(v))
	if len(units) <= n {
		return v
	}
	return string(utf16.Decode(units[:n]))
}
func BoolParam(v string) bool { return v == "1" || v == "true" }

// Truthy applies JavaScript truthiness to decoded JSON values.
func Truthy(v any) bool {
	if n, ok := v.(float64); ok && math.IsNaN(n) {
		return false
	}
	return NonzeroTruthy(v)
}

// NonzeroTruthy retains discovery's NaN-is-nonzero compatibility behavior.
func NonzeroTruthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}
func ActorID(c *Ctx) int64 {
	if c.Me != nil {
		return c.Me.MID
	}
	return 0
}

// UTF16PrefixNormalized also replaces invalid UTF-8 in an uncut string, as the
// live and moderation paths did by always round-tripping through UTF-16.
func UTF16PrefixNormalized(v string, n int) string { return string([]rune(UTF16Prefix(v, n))) }
