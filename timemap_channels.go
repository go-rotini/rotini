package rotini

import (
	"maps"
	"slices"
	"strings"

	"github.com/go-rotini/recon"
)

// timeMapValue reads a map[string]time.Time input's value under its declared layouts or
// relative form, which recon's own decoding (RFC 3339 only) does not know: an environment
// variable's key=value pairs, a configuration file's map, or one of the map's leaves (path is
// <key>.<name>). ok is false when path is no such input, or when a value does not parse, so
// recon reports it as a usage error naming the input.
func (s spellings) timeMapValue(path recon.Path, v recon.Value) (recon.Value, bool) {
	if len(s.keys.timeMaps) == 0 {
		return v, false
	}
	key := path.String()
	if ts, ok := s.keys.timeMaps[key]; ok {
		ts.clock = s.clock
		return timeMapOf(v, ts)
	}
	parent, _, cut := strings.Cut(key, ".")
	if ts, ok := s.keys.timeMaps[parent]; ok && cut && v.Kind() == recon.StringKind {
		ts.clock = s.clock
		t, err := parseTimeValue(v.String(), ts)
		if err != nil {
			return v, false
		}
		return recon.NewValue(t), true
	}
	return v, false
}

// timeMapOf converts a whole time map: "k=v,k2=v2" text, or a map of text values.
func timeMapOf(v recon.Value, ts timeSpec) (recon.Value, bool) {
	out := map[string]any{}
	switch v.Kind() {
	case recon.StringKind:
		for entry := range strings.SplitSeq(v.String(), ",") {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			k, text, ok := strings.Cut(entry, "=")
			if !ok {
				return v, false // checkEnvMaps reports a malformed pair
			}
			t, err := parseTimeValue(strings.TrimSpace(text), ts)
			if err != nil {
				return v, false
			}
			out[strings.TrimSpace(k)] = t
		}
	case recon.MapKind:
		m, err := v.AsMap()
		if err != nil {
			return v, false
		}
		for k, e := range m {
			if _, isTime := e.Any().(interface{ Unix() int64 }); isTime {
				out[k] = e.Any()
				continue
			}
			t, err := parseTimeValue(e.String(), ts)
			if err != nil {
				return v, false
			}
			out[k] = t
		}
	default:
		return v, false
	}
	return recon.NewValue(out), true
}

// Keys adds each map input's own key to the source's keys when the source holds a map there. A
// configuration file lists only a map's leaves (labels.team), so without its key the registry
// would never hand the map to the input that reads it whole.
func (s spellings) Keys() []recon.Path {
	keys := s.Source.Keys()
	if len(s.keys.maps) == 0 {
		return keys
	}
	listed := make(map[string]bool, len(keys))
	for _, k := range keys {
		listed[k.String()] = true
	}
	for _, key := range slices.Sorted(maps.Keys(s.keys.maps)) {
		if listed[key] {
			continue
		}
		p := recon.ParsePath(key)
		if v, found, err := s.Source.Get(p); err == nil && found && v.Kind() == recon.MapKind {
			keys = append(keys, p)
		}
	}
	return keys
}
