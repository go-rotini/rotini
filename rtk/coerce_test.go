package rtk_test

import (
	"errors"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"testing"
	"time"

	"github.com/go-rotini/rotini/rtk"
)

// coerceCase describes one row of the type/value/expected-value matrix.
// Either want is a concrete expected value (compared via reflect.DeepEqual)
// or wantErr is set (in which case the case must return an error).
type coerceCase struct {
	name    string
	typ     string
	value   string
	want    any
	wantErr bool
}

func TestCoerceBuiltinValue_string(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"basic", "string", "hello", "hello", false},
		{"empty", "string", "", "", false},
		{"whitespace", "string", "  spaces  ", "  spaces  ", false},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_bool(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"true", "bool", "true", true, false},
		{"True", "bool", "True", true, false},
		{"TRUE", "bool", "TRUE", true, false},
		{"false", "bool", "false", false, false},
		{"empty_is_false", "bool", "", false, false},
		{"junk_is_false", "bool", "garbage", false, false},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_intTypes(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"int_positive", "int", "42", 42, false},
		{"int_negative", "int", "-7", -7, false},
		{"int_zero", "int", "0", 0, false},
		{"int_garbage", "int", "abc", nil, true},
		{"int_overflow_caught_by_strconv", "int", "999999999999999999999", nil, true},

		{"int32_positive", "int32", "100", int32(100), false},
		{"int32_overflow", "int32", "99999999999", nil, true},
		{"int32_garbage", "int32", "x", nil, true},

		{"int64_positive", "int64", "9000000000", int64(9000000000), false},
		{"int64_negative", "int64", "-9000000000", int64(-9000000000), false},
		{"int64_garbage", "int64", "x", nil, true},

		{"uint_positive", "uint", "10", uint(10), false},
		{"uint_negative_fails", "uint", "-5", nil, true},
		{"uint32_positive", "uint32", "10", uint32(10), false},
		{"uint64_positive", "uint64", "10", uint64(10), false},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_floatTypes(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"float_positive", "float", "3.14", 3.14, false},
		{"float_negative", "float", "-3.14", -3.14, false},
		{"float_zero", "float", "0", float64(0), false},
		{"float_scientific", "float", "1e3", float64(1000), false},
		{"float_garbage", "float", "x", nil, true},

		{"float64_alias", "float64", "2.718", 2.718, false},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_duration(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"seconds", "duration", "5s", 5 * time.Second, false},
		{"minutes", "duration", "2m30s", 2*time.Minute + 30*time.Second, false},
		{"hours_alias", "time.Duration", "1h", time.Hour, false},
		{"zero", "duration", "0", time.Duration(0), false},
		{"garbage", "duration", "x", nil, true},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_time(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"rfc3339", "time.Time", "2026-05-18T12:00:00Z",
			time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC), false},
		{"date_only", "time.Time", "2026-05-18",
			time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), false},
		{"slash_format", "time.Time", "05/18/2026",
			time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC), false},
		{"garbage", "time.Time", "not a time", nil, true},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_sliceTypes(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"strings_csv", "[]string", "a,b,c", []string{"a", "b", "c"}, false},
		{"strings_trimmed", "[]string", "a, b , c", []string{"a", "b", "c"}, false},
		{"strings_single", "[]string", "solo", []string{"solo"}, false},
		{"strings_empty", "[]string", "", []string{""}, false},

		{"ints_csv", "[]int", "1,2,3", []int{1, 2, 3}, false},
		{"ints_trimmed", "[]int", "1, 2 , 3", []int{1, 2, 3}, false},
		{"ints_garbage", "[]int", "1,x,3", nil, true},

		{"floats_csv", "[]float", "1.5,2.5", []float64{1.5, 2.5}, false},
		{"floats_garbage", "[]float", "1.5,x", nil, true},
		{"floats_alias", "[]float64", "0.1,0.2", []float64{0.1, 0.2}, false},

		{"bools_csv", "[]bool", "true,false,True", []bool{true, false, true}, false},
		{"bools_garbage_is_false", "[]bool", "true,junk,false", []bool{true, false, false}, false},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_mapTypes(t *testing.T) {
	t.Parallel()
	cases := []coerceCase{
		{"strings_kv", "map[string]string", "key=value", map[string]string{"key": "value"}, false},
		{"strings_empty_val_ok", "map[string]string", "key=", map[string]string{"key": ""}, false},
		{"strings_missing_eq", "map[string]string", "novalue", nil, true},
		{"strings_empty_key_fails", "map[string]string", "=value", nil, true},

		{"ints_kv", "map[string]int", "n=42", map[string]int{"n": 42}, false},
		{"ints_bad_val", "map[string]int", "n=x", nil, true},

		{"bools_kv", "map[string]bool", "flag=true", map[string]bool{"flag": true}, false},
		{"bools_case_insensitive", "map[string]bool", "flag=True", map[string]bool{"flag": true}, false},

		{"floats_kv", "map[string]float64", "ratio=0.5", map[string]float64{"ratio": 0.5}, false},
		{"floats_bad_val", "map[string]float64", "ratio=x", nil, true},
	}
	runCoerceCases(t, cases)
}

func TestCoerceBuiltinValue_specialTypes(t *testing.T) {
	t.Parallel()

	// net.IP
	if got, _, err := rtk.CoerceBuiltinValue("net.IP", "192.168.1.1"); err != nil {
		t.Errorf("net.IP coerce: unexpected error %v", err)
	} else if ip, ok := got.(net.IP); !ok || ip.String() != "192.168.1.1" {
		t.Errorf("net.IP coerce: got %v, want 192.168.1.1", got)
	}
	if _, _, err := rtk.CoerceBuiltinValue("net.IP", "not.an.ip"); err == nil {
		t.Error("net.IP coerce: expected error for invalid IP")
	}

	// *url.URL
	if got, _, err := rtk.CoerceBuiltinValue("*url.URL", "https://example.com/path"); err != nil {
		t.Errorf("*url.URL coerce: unexpected error %v", err)
	} else if u, ok := got.(*url.URL); !ok || u.Host != "example.com" {
		t.Errorf("*url.URL coerce: got %v, want host=example.com", got)
	}

	// *regexp.Regexp
	if got, _, err := rtk.CoerceBuiltinValue("*regexp.Regexp", "^[a-z]+$"); err != nil {
		t.Errorf("*regexp.Regexp coerce: unexpected error %v", err)
	} else if r, ok := got.(*regexp.Regexp); !ok || !r.MatchString("hello") {
		t.Errorf("*regexp.Regexp coerce: got %v, want compiled regex matching 'hello'", got)
	}
	if _, _, err := rtk.CoerceBuiltinValue("*regexp.Regexp", "[invalid"); err == nil {
		t.Error("*regexp.Regexp coerce: expected error for invalid regex")
	}
}

func TestCoerceBuiltinValue_unknownTypeReturnsSentinel(t *testing.T) {
	t.Parallel()
	got, _, err := rtk.CoerceBuiltinValue("UserDefinedType", "value")
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
	if !errors.Is(err, rtk.ErrUnknownType) {
		t.Errorf("err: got %v, want ErrUnknownType", err)
	}
	if !rtk.IsUnknownType(err) {
		t.Error("IsUnknownType returned false for ErrUnknownType")
	}
	if rtk.IsUnknownType(nil) {
		t.Error("IsUnknownType returned true for nil")
	}
}

// runCoerceCases is the shared driver. It runs CoerceBuiltinValue for each
// case and compares results via reflect.DeepEqual.
func runCoerceCases(t *testing.T, cases []coerceCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, _, err := rtk.CoerceBuiltinValue(tc.typ, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Errorf("got %v, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v (%T), want %v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}
