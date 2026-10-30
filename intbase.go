package rotini

import (
	"encoding"
	"reflect"
	"strconv"
	"strings"
)

// basePrefixed is the value of [FlagDef.Base] and [ArgDef.Base] (and an env or config field's
// base:"prefixed" tag) that lets a number input take 0x, 0o and 0b literals, and a float input
// hexadecimal floats and `_` between digits.
const basePrefixed = "prefixed"

// prefixedDecimal reads s as an integer literal that may carry a base prefix after an optional
// sign: 0x or 0X (hexadecimal), 0o or 0O (octal), 0b or 0B (binary). `_` may separate digits,
// and follow the prefix, as in Go literals. Without a prefix s is decimal, leading zeros and
// all: "010" is ten. It returns the value in decimal, or false for anything else, including a
// magnitude beyond 64 bits, which the caller reports as written.
func prefixedDecimal(s string) (string, bool) {
	sign, body := "", s
	if body != "" && (body[0] == '+' || body[0] == '-') {
		sign, body = body[:1], body[1:]
	}
	var mag uint64
	var err error
	if len(body) > 2 && body[0] == '0' && strings.ContainsRune("xXoObB", rune(body[1])) {
		mag, err = strconv.ParseUint(body, 0, 64) // base 0 reads the prefix and its `_` rules
	} else {
		if !decimalDigits(body) {
			return "", false
		}
		mag, err = strconv.ParseUint(strings.ReplaceAll(body, "_", ""), 10, 64)
	}
	if err != nil {
		return "", false
	}
	d := strconv.FormatUint(mag, 10)
	if sign == "-" && mag != 0 {
		d = "-" + d
	}
	return d, true
}

// decimalDigits reports whether s is decimal digits with single underscores between them.
func decimalDigits(s string) bool {
	if s == "" || s[0] == '_' || s[len(s)-1] == '_' || strings.Contains(s, "__") {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// prefixedNumber is [parseNumber] for an input that declares base: prefixed: it also reads a
// prefixed integer, a hexadecimal float (0x1p4) and `_` between digits.
func prefixedNumber(v string) (float64, bool) {
	if d, ok := prefixedDecimal(v); ok {
		v = d
	}
	n, err := strconv.ParseFloat(v, 64)
	return n, err == nil
}

// plainFloatText reports whether s is free of what only base: prefixed lets a float take: a
// hexadecimal mantissa (0x1p4) and `_` between digits. inf and nan in any case are plain.
func plainFloatText(s string) bool {
	if strings.IndexByte(s, '_') >= 0 {
		return false
	}
	body := strings.TrimLeft(s, "+-")
	return len(body) < 2 || body[0] != '0' || (body[1] != 'x' && body[1] != 'X')
}

// prefixedFloatText spells v, a float under base: prefixed, as a plain decimal float when it
// reads as a prefixed integer, a hexadecimal float or digits with `_`, and as written
// otherwise.
func prefixedFloatText(v string) string {
	if plainFloatText(v) {
		return v
	}
	if n, ok := prefixedNumber(v); ok {
		return strconv.FormatFloat(n, 'g', -1, 64)
	}
	return v
}

// prefixedText spells v in decimal when it reads as a prefixed integer, and as written
// otherwise, for comparing values as the input reads them.
func prefixedText(v string) string {
	if d, ok := prefixedDecimal(v); ok {
		return d
	}
	return v
}

// isPlainInteger reports whether t (through pointers and slices) holds a plain integer: an int
// or uint kind that is neither a duration nor a type that parses its own text.
func isPlainInteger(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
	default:
		return false
	}
	return t != durationType && !reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

// coercePrefixed is [coerce] for a number input that declares base: prefixed: each value is
// read by [prefixedDecimal] (an integer) or [prefixedFloatText] (a float) first, and an error
// names the value as written.
func coercePrefixed(f reflect.Value, raw []string, fs *featureSet) error {
	if f.Kind() == reflect.Slice {
		out := reflect.MakeSlice(f.Type(), len(raw), len(raw))
		for i, r := range raw {
			if err := coercePrefixed(out.Index(i), []string{r}, fs); err != nil {
				return err
			}
		}
		f.Set(out)
		return nil
	}
	last := raw[len(raw)-1]
	d, ok := prefixedDecimal(last)
	if isPlainFloat(f.Type()) {
		d, ok = prefixedFloatText(last), true
	}
	if !ok {
		return coerce(f, []string{last}, fs)
	}
	err := coerce(f, []string{d}, fs)
	if ce, isCoerce := asError[*coerceError](err); isCoerce {
		ce.Value = last
	}
	return err
}

// plainDecimalText is s, a decimal integer with an optional sign, without leading zeros, so a
// reader that takes Go literals can't read "010" as octal. Anything else is returned as it is.
func plainDecimalText(s string) string {
	sign, digits := "", s
	if s != "" && (s[0] == '+' || s[0] == '-') {
		sign, digits = s[:1], s[1:]
	}
	if digits == "" || strings.TrimLeft(digits, "0123456789") != "" {
		return s
	}
	if trimmed := strings.TrimLeft(digits, "0"); trimmed != digits {
		if trimmed == "" {
			trimmed = "0"
		}
		return sign + trimmed
	}
	return s
}

// isPlainFloat reports whether t (through pointers and slices) holds a plain float: a float
// kind that is not a type that parses its own text.
func isPlainFloat(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	return (t.Kind() == reflect.Float32 || t.Kind() == reflect.Float64) &&
		!reflect.PointerTo(t).Implements(reflect.TypeFor[encoding.TextUnmarshaler]())
}

// plainIntegerField reports whether t is an integer field, or a list of them, whose text
// [plainDecimalText] reads. A plain []uint8 is a list of integers, as on the command line; a
// named byte type ([HexBytes], [Base64Bytes]) holds bytes.
func plainIntegerField(t reflect.Type) bool {
	ft := derefType(t)
	return isPlainInteger(ft) && (ft.Kind() != reflect.Slice || ft.Elem().Kind() != reflect.Uint8 || ft.Name() == "")
}
