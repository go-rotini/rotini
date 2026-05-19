package rtk

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// CoerceValueFn converts a string value to the appropriate Go type for a
// given declared type name.
//
// It returns:
//   - the coerced value (typed),
//   - a "consumed-next-arg" flag — for inline values this is false; the
//     parser's resolution loop uses it when distinguishing inline (`-x=1`)
//     vs. separate (`-x 1`) flag-value forms, and
//   - an error wrapping the underlying conversion failure, or nil on success.
//
// Custom coercers can intercept unknown types and dispatch to user-defined
// logic. The convention: if a coercer cannot handle the given type, it
// returns (nil, false, [ErrUnknownType]) and the caller falls through to
// the built-in coercer.
type CoerceValueFn func(declaredType, value string) (parsed any, consumedNext bool, err error)

// CoerceBuiltinValue converts value to a Go value of the requested
// declaredType.
//
// Supported declared types:
//
//	string, bool, int, int32, int64, uint, uint32, uint64,
//	float, float64, duration, time.Duration, time.Time,
//	[]string, []int, []float, []float64, []bool,
//	map[string]string, map[string]int, map[string]bool, map[string]float64,
//	net.IP, *url.URL, *regexp.Regexp
//
// For unknown types it returns ([nil], false, [ErrUnknownType]). For known
// types that fail to coerce, the returned error wraps the underlying parser
// error (typically from strconv or net/url).
//
// The consumedNext return is always false here; the value is taken to be a
// single string passed in by the caller. The flag-resolution layer uses it
// only to indicate when an inline value was consumed.
func CoerceBuiltinValue(declaredType, value string) (any, bool, error) {
	switch declaredType {
	case "string":
		return value, false, nil
	case "bool":
		return strings.EqualFold(value, "true"), false, nil
	case "int":
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, false, fmt.Errorf("invalid integer value: %s", value)
		}
		return n, false, nil
	case "int32":
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			return nil, false, fmt.Errorf("invalid int32 value: %s", value)
		}
		return int32(n), false, nil
	case "int64":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid int64 value: %s", value)
		}
		return n, false, nil
	case "uint":
		n, err := strconv.ParseUint(value, 10, 0)
		if err != nil {
			return nil, false, fmt.Errorf("invalid uint value: %s", value)
		}
		return uint(n), false, nil
	case "uint32":
		n, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return nil, false, fmt.Errorf("invalid uint32 value: %s", value)
		}
		return uint32(n), false, nil
	case "uint64":
		n, err := strconv.ParseUint(value, 10, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid uint64 value: %s", value)
		}
		return n, false, nil
	case "float", "float64":
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid float value: %s", value)
		}
		return f, false, nil
	case "duration", "time.Duration":
		d, err := time.ParseDuration(value)
		if err != nil {
			return nil, false, fmt.Errorf("invalid duration value: %s", value)
		}
		return d, false, nil
	case "time.Time":
		for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05", "01/02/2006"} {
			if t, err := time.Parse(layout, value); err == nil {
				return t, false, nil
			}
		}
		return nil, false, fmt.Errorf("invalid time value %q (use RFC3339 or YYYY-MM-DD)", value)
	case "[]string":
		parts := strings.Split(value, ",")
		out := make([]string, len(parts))
		for i, p := range parts {
			out[i] = strings.TrimSpace(p)
		}
		return out, false, nil
	case "[]int":
		parts := strings.Split(value, ",")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, false, fmt.Errorf("invalid int in list: %s", p)
			}
			out = append(out, n)
		}
		return out, false, nil
	case "[]float", "[]float64":
		parts := strings.Split(value, ",")
		out := make([]float64, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			f, err := strconv.ParseFloat(p, 64)
			if err != nil {
				return nil, false, fmt.Errorf("invalid float in list: %s", p)
			}
			out = append(out, f)
		}
		return out, false, nil
	case "[]bool":
		parts := strings.Split(value, ",")
		out := make([]bool, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.EqualFold(strings.TrimSpace(p), "true"))
		}
		return out, false, nil
	case "map[string]string":
		k, v, ok := strings.Cut(value, "=")
		if !ok || k == "" {
			return nil, false, fmt.Errorf("invalid map value: %s (expected key=value)", value)
		}
		return map[string]string{k: v}, false, nil
	case "map[string]int":
		k, v, ok := strings.Cut(value, "=")
		if !ok || k == "" {
			return nil, false, fmt.Errorf("invalid map value: %s (expected key=value)", value)
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, false, fmt.Errorf("invalid integer value for map key %q: %s", k, v)
		}
		return map[string]int{k: n}, false, nil
	case "map[string]bool":
		k, v, ok := strings.Cut(value, "=")
		if !ok || k == "" {
			return nil, false, fmt.Errorf("invalid map value: %s (expected key=value)", value)
		}
		return map[string]bool{k: strings.EqualFold(v, "true")}, false, nil
	case "map[string]float64":
		k, v, ok := strings.Cut(value, "=")
		if !ok || k == "" {
			return nil, false, fmt.Errorf("invalid map value: %s (expected key=value)", value)
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, false, fmt.Errorf("invalid float value for map key %q: %s", k, v)
		}
		return map[string]float64{k: f}, false, nil
	case "net.IP":
		ip := net.ParseIP(value)
		if ip == nil {
			return nil, false, fmt.Errorf("invalid IP address: %s", value)
		}
		return ip, false, nil
	case "*url.URL":
		u, err := url.Parse(value)
		if err != nil {
			return nil, false, fmt.Errorf("invalid URL: %s", value)
		}
		return u, false, nil
	case "*regexp.Regexp":
		r, err := regexp.Compile(value)
		if err != nil {
			return nil, false, fmt.Errorf("invalid regexp %q: %w", value, err)
		}
		return r, false, nil
	default:
		return nil, false, ErrUnknownType
	}
}

// IsUnknownType reports whether err is or wraps [ErrUnknownType]. Convenience
// wrapper around [errors.Is].
func IsUnknownType(err error) bool { return errors.Is(err, ErrUnknownType) }
