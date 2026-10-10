package codegen

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Placement hints for the two ways an input key lands one level off: a schema key written on
// the input entry (`key:` beside `schema:`), and an entry key written inside `schema:`.

// inputDefinitionChannels maps each input entry's schema definition to its channel.
var inputDefinitionChannels = map[string]string{
	"FlagInput": "flag", "ArgumentInput": "argument", "EnvInput": "env",
	"ConfigInput": "config", "StdinSpec": "stdin",
}

// schemaKeyChannels lists the channels a channel-specific schema key works on, matching the
// lint rules that reject it elsewhere. A schema key not listed works on every channel.
var schemaKeyChannels = map[string][]string{
	"negatable":      {"flag"},
	"implicit_value": {"flag"},
	"dotted_keys":    {"flag"},
	"from":           {"flag", "argument"},
	"properties":     {"flag", "stdin"},
	"separator":      {"flag", "argument", "env"},
	"complete":       {"flag", "argument"},
	"variable":       {"flag", "argument", "env"},
	"variable_file":  {"flag", "argument", "env"},
	"config_source":  {"flag", "env"},
	"key":            {"flag", "argument", "config"},
	"nesting":        {"env"},
	"file":           {"config"},
	"repeatable":     {"flag"},
	"glob":           {"argument"},
	"expand":         {"flag", "argument", "env", "config"},
	"relative":       {"flag", "argument", "env", "config"},
}

// channelPlurals names each channel's entries in a hint.
var channelPlurals = map[string]string{
	"flag": "flags", "argument": "arguments", "env": "env inputs", "config": "config inputs", "stdin": "stdin",
}

// schemaKeyFits reports whether key is a schema key that takes effect on channel.
func schemaKeyFits(channel, key string) bool {
	sets, err := loadSchemaBlockKeys()
	if err != nil || !sets.input[key] {
		return false
	}
	chans, limited := schemaKeyChannels[key]
	return !limited || slices.Contains(chans, channel)
}

// schemaPlacementHint returns the hint for an unknown key on the input entry named by
// definition: where the key belongs when it is a schema key that works on this channel, which
// channels take it when it works on others only, or "".
func schemaPlacementHint(definition, key string) string {
	channel, ok := inputDefinitionChannels[definition]
	if !ok {
		return ""
	}
	if schemaKeyFits(channel, key) {
		return fmt.Sprintf("%q belongs under schema: (schema: {%s: …})", key, key)
	}
	sets, err := loadSchemaBlockKeys()
	chans, limited := schemaKeyChannels[key]
	if err != nil || !sets.input[key] || !limited {
		return ""
	}
	names := make([]string, len(chans))
	for i, c := range chans {
		names[i] = channelPlurals[c]
	}
	return fmt.Sprintf("%q is a schema key of %s only", key, joinAnd(names))
}

// entryPlacementHint returns the hint for an unknown key inside channel's schema block when
// the key belongs on the entry itself, beside schema:, or "".
func entryPlacementHint(channel, key string) string {
	keys := loadEntryKeys()[channel]
	if keys == nil || !keys[key] {
		return ""
	}
	where := "the " + channel + " input"
	switch channel {
	case "flag", "argument":
		where = "the " + channel
	case "stdin":
		where = "stdin"
	}
	return fmt.Sprintf("%q belongs on %s itself, beside schema:", key, where)
}

// loadEntryKeys returns each channel's entry keys (the input definition's properties other
// than schema), read from the embedded spec schema.
var loadEntryKeys = sync.OnceValue(func() map[string]map[string]bool {
	var doc struct {
		Definitions map[string]struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"definitions"`
	}
	out := map[string]map[string]bool{}
	if json.Unmarshal(schemaSpecFileBytes, &doc) != nil {
		return out
	}
	for def, channel := range inputDefinitionChannels {
		keys := map[string]bool{}
		for k := range doc.Definitions[def].Properties {
			if k != "schema" {
				keys[k] = true
			}
		}
		out[channel] = keys
	}
	return out
})

// joinAnd joins words as a prose list: "a", "a and b", "a, b and c".
func joinAnd(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}
