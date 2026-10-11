package rotini

import (
	"strconv"
	"strings"
)

// inferScalar types key=value text the schema says nothing about (inside a free-form object or
// a dotted_keys map) as its JSON spelling would decode: true and false are booleans, null is
// nil, a JSON number is a float64, and anything else is the text itself. This keeps
// `replicas=5` and `{"replicas":5}` equivalent. Only JSON scalar forms are inferred; "007",
// "1_000", "yes" and "0x10" stay text, and so does a number too large for a float64.
func inferScalar(text string) any {
	switch text {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if text == "" || !strings.ContainsAny(text[:1], "-0123456789") || strings.TrimSpace(text) != text {
		return text
	}
	r := jsonReader{s: text}
	if !r.number() || r.i != len(text) {
		return text
	}
	if n, err := strconv.ParseFloat(text, 64); err == nil {
		return n
	}
	return text
}
