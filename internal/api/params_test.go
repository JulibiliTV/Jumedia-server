package api

import (
	"math"
	"reflect"
	"testing"
)

// These cases were run against the domain helpers before consolidation.
func TestParameterCompatibility(t *testing.T) {
	parsers := []struct {
		name  string
		parse func(string, float64) float64
		wants []float64
	}{
		{"discover", NumberUnicode, []float64{9, 0, 9, 7, 9, 9, 9, 9}},
		{"space", NumberWide, []float64{9, 0, 7, 9, 9, 9, 9, math.Exp2(64)}},
		{"community", Number, []float64{9, 0, 7, 9, 9, 9, 9, 9}},
		{"pgc", NumberUnicode, []float64{9, 0, 9, 7, 9, 9, 9, 9}},
		{"creator", NumberGo, []float64{9, 0, 7, 9, 10, 1, 9, 9}},
		{"live", NumberUnicodeGoFloat, []float64{9, 0, 9, 7, 10, 1, 4, 9}},
		{"admin", func(s string, d float64) float64 { return NumberUnicodeGo(s, d) }, []float64{9, 0, 9, 7, 10, 1, 9, 9}},
	}
	inputs := []string{"", " ", "\ufeff7\ufeff", "\u00857\u0085", "1_0", "0x_1", "0x1p2", "0x10000000000000000"}
	for _, p := range parsers {
		t.Run(p.name, func(t *testing.T) {
			for i, input := range inputs {
				if got := p.parse(input, 9); got != p.wants[i] {
					t.Errorf("%q = %v; want %v", input, got, p.wants[i])
				}
			}
			for input, want := range map[string]float64{"1.5": 1.5, "1e2": 100, "-2": -2, "0b11": 3, "0o11": 9, "0x11": 17, "Infinity": 9, "NaN": 9, "no": 9, "1e999": 9} {
				if got := p.parse(input, 9); got != want {
					t.Errorf("%q = %v; want %v", input, got, want)
				}
			}
		})
	}
	if !math.IsInf(RawNumber("Infinity"), 1) || !math.IsNaN(RawNumber("bad")) {
		t.Fatal("raw Number must preserve nonfinite values")
	}
	for _, n := range []float64{1.5, math.Exp2(63), -math.Exp2(64)} {
		if DBID(n) != nil {
			t.Fatal("invalid DB ID")
		}
	}
	if ParseWideID("1.5") != 0 || ParseID("1.5") != math.MinInt64 || ParseGoID("1.5") != nil || NonnegativeID(-1) != 0 || ParseWideID("-1") != -1 {
		t.Fatal("ID sentinel compatibility")
	}
	if !NonzeroTruthy(math.NaN()) || Truthy(math.NaN()) || BoolParam("yes") || !BoolParam("1") {
		t.Fatal("truthiness compatibility")
	}
}

func TestParameterPagingAndText(t *testing.T) {
	rows := []int{0, 1, 2, 3, 4}
	pages := []func([]int, float64, float64) []int{Page[int]}
	for _, page := range pages {
		for _, tt := range []struct {
			pn, ps float64
			want   []int
		}{{0, 2, []int{0, 1}}, {1.5, 2, []int{1, 2}}, {1, -2, []int{0, 1, 2}}, {2, 2, []int{2, 3}}, {1e100, 2, []int{}}} {
			if got := page(rows, tt.pn, tt.ps); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("page(%v,%v)=%v", tt.pn, tt.ps, got)
			}
		}
	}
	for _, trim := range []func(string) string{Trim} {
		if trim("\ufeff x \ufeff") != "x" || trim("\u0085") != "\u0085" {
			t.Fatal("JS trim")
		}
	}
	for _, length := range []func(string) int{UTF16Length} {
		if length("a😀中") != 4 {
			t.Fatal("UTF16 length")
		}
	}
	for _, slice := range []func(string, int) string{UTF16Prefix, UTF16PrefixNormalized} {
		if slice("a😀中", 2) != "a�" || slice("a😀中", 3) != "a😀" {
			t.Fatal("UTF16 prefix")
		}
	}
}

func TestUTF16NormalizationCompatibility(t *testing.T) {
	invalid := string([]byte{0xff, 0xfe})
	if UTF16Prefix(invalid, 40) != invalid {
		t.Fatal("account prefix must preserve an uncut string")
	}
	if UTF16PrefixNormalized(invalid, 40) != "��" {
		t.Fatal("live/log prefix must normalize invalid UTF8")
	}
}
