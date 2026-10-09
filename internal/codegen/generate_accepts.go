package codegen

import "strings"

// acceptsNote says what a time input accepts when it is more than the default for its type,
// for the docs pages: its first declared layout, and its relative forms. "" for any other
// input, and for a time input that declares neither.
//
//	accepts 02/01/2006
//	accepts RFC 3339 or a time ago: 2h, 3d, yesterday
func acceptsNote(schema *InputSchema) string {
	declared := declaredLayouts(schema)
	rel := relative(schema)
	if len(declared) == 0 && rel == "" {
		return ""
	}
	if t := strings.TrimPrefix(getSchemaType(schema), "[]"); t != "time.Time" && t != "*time.Time" {
		return "" // lint reports a layout or relative on a non-time input
	}
	layouts := layoutsFor(schema)
	abs := "RFC 3339"
	if len(layouts) > 0 {
		abs = describeLayout(layouts[0])
	}
	if rel == "" {
		return "accepts " + abs
	}
	date := isDateOnly(layouts)
	samples := "2h, 3d"
	if date {
		samples = "3d, 1w"
	}
	switch rel {
	case "past":
		return "accepts " + abs + " or a time ago: " + samples + ", yesterday"
	case "future":
		return "accepts " + abs + " or a time from now: " + samples + ", tomorrow"
	}
	signed := "-" + strings.ReplaceAll(samples, ", ", ", +")
	return "accepts " + abs + " or a signed offset: " + signed + ", today"
}

// describeLayout names a layout for a page: a Go layout as written, unix and unixmilli by what
// they mean.
func describeLayout(l string) string {
	switch l {
	case "unix":
		return "a Unix timestamp in seconds"
	case "unixmilli":
		return "a Unix timestamp in milliseconds"
	}
	return l
}

// isDateOnly reports whether every layout writes a calendar date and no time of day, as the
// runtime decides when a relative value lands on a date.
func isDateOnly(layouts []string) bool {
	if len(layouts) == 0 {
		return false
	}
	for _, l := range layouts {
		if l == "unix" || l == "unixmilli" {
			return false
		}
		t, ok := readLayout(l, sampleTime.Format(l))
		if !ok {
			return false
		}
		if h, m, s := t.Clock(); h != 0 || m != 0 || s != 0 || t.Nanosecond() != 0 {
			return false
		}
	}
	return true
}

// argumentFallback is an argument's env fallback variables, in lookup order, and its config
// key when the command reads config files: the names help shows and the generated tags pin.
func argumentFallback(a ArgumentInput, envPrefix string, readsConfig bool) (env []string, configKey string) {
	key := flagReconKey(a.Name, a.Schema)
	if key == "" {
		return nil, ""
	}
	if v := flagEnvVar(a.Schema, key, envPrefix); v != "" {
		env = strings.Split(v, ",")
	}
	if readsConfig {
		configKey = key
	}
	return env, configKey
}
