package codegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// The description an importer writes (github.com/go-rotini/import): a program's command tree
// in the shape of a spec, with notes on what could not be carried across and pointers to the
// hooks to port. These types mirror the importer's, so rotini's module never depends on it.

// importFormat is the description format, and the major version, this rotini reads.
const importFormat = "rotini-import/1"

type importResult struct {
	Format  string         `json:"format"`
	Source  importSource   `json:"source"`
	Command *importCommand `json:"command"`
	Notes   []importNote   `json:"notes"`
	Hooks   []importHook   `json:"hooks"`
}

type importSource struct {
	Framework string `json:"framework"`
	Version   string `json:"version"`
	Importer  string `json:"importer"`
}

type importCommand struct {
	Name        string             `json:"name"`
	DisplayName string             `json:"display_name"`
	Aliases     []string           `json:"aliases"`
	Summary     string             `json:"summary"`
	Description string             `json:"description"`
	Examples    []string           `json:"examples"`
	Group       string             `json:"group"`
	Hidden      bool               `json:"hidden"`
	Deprecated  string             `json:"deprecated"`
	Passthrough bool               `json:"passthrough"`
	Arguments   []*importArgument  `json:"arguments"`
	Flags       []*importFlag      `json:"flags"`
	FlagGroups  []*importFlagGroup `json:"flag_groups"`
	Commands    []*importCommand   `json:"commands"`
	// Builtin marks a command the framework adds itself: "help" or "completion".
	Builtin string `json:"builtin"`
}

type importArgument struct {
	Name    string        `json:"name"`
	Summary string        `json:"summary"`
	Schema  *importSchema `json:"schema"`
}

type importFlag struct {
	Name                  string        `json:"name"`
	Summary               string        `json:"summary"`
	Identifiers           []string      `json:"identifiers"`
	Cascading             bool          `json:"cascading"`
	ShortCircuit          bool          `json:"short_circuit"`
	Hidden                bool          `json:"hidden"`
	Deprecated            string        `json:"deprecated"`
	DeprecatedIdentifiers []string      `json:"deprecated_identifiers"`
	Schema                *importSchema `json:"schema"`
	// Builtin marks a flag the framework adds itself: "help" or "version".
	Builtin string `json:"builtin"`
}

type importSchema struct {
	Type          string            `json:"type"`
	Items         *importSchema     `json:"items"`
	Required      bool              `json:"required"`
	Default       any               `json:"default"`
	Enum          []importEnumValue `json:"enum"`
	MinItems      *int              `json:"minItems"`
	MaxItems      *int              `json:"maxItems"`
	Separator     string            `json:"separator"`
	ImplicitValue string            `json:"implicit_value"`
	Placeholder   string            `json:"placeholder"`
	Complete      *importComplete   `json:"complete"`
}

type importEnumValue struct {
	Value   string `json:"value"`
	Summary string `json:"summary"`
}

type importComplete struct {
	Kind       string   `json:"kind"`
	Extensions []string `json:"extensions"`
}

type importFlagGroup struct {
	Kind  string   `json:"kind"`
	Flags []string `json:"flags"`
}

type importNote struct {
	Path  string `json:"path"`
	Level string `json:"level"`
	Msg   string `json:"msg"`
}

// String is the note's stderr line: "[lossy] acme deploy: …".
func (n importNote) String() string { return fmt.Sprintf("[%s] %s: %s", n.Level, n.Path, n.Msg) }

type importHook struct {
	Path      string `json:"path"`
	Framework string `json:"framework"`
	Rotini    string `json:"rotini"`
	Func      string `json:"func"`
	File      string `json:"file"`
	Line      int    `json:"line"`
}

// readImportResult decodes a description, refusing one in another format or major version.
// Numbers keep their text (json.Number), so a default is written exactly as the importer
// wrote it.
func readImportResult(r io.Reader) (*importResult, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var res importResult
	if err := dec.Decode(&res); err != nil {
		return nil, fmt.Errorf("read the importer's description: %w", err)
	}
	rest, ok := strings.CutPrefix(res.Format, "rotini-import/")
	major, _, _ := strings.Cut(rest, ".")
	if !ok || major != strings.TrimPrefix(importFormat, "rotini-import/") {
		return nil, fmt.Errorf("the importer wrote a %q description, but this rotini reads %q; pass an --importer-version that writes it", res.Format, importFormat)
	}
	if res.Command == nil {
		return nil, errors.New("the importer's description has no command")
	}
	return &res, nil
}

// stats is the import's summary line, counting r's notes and extra:
// "imported 13 commands, 41 flags: 9 lossy, 2 unsupported, 7 info".
func (r *importResult) stats(extra []importNote) string {
	var commands, flags int
	var walk func(c *importCommand)
	walk = func(c *importCommand) {
		commands++
		flags += len(c.Flags)
		for _, sub := range c.Commands {
			walk(sub)
		}
	}
	walk(r.Command)
	levels := map[string]int{}
	for _, n := range append(slices.Clone(r.Notes), extra...) {
		levels[n.Level]++
	}
	return fmt.Sprintf("imported %d commands, %d flags: %d lossy, %d unsupported, %d info",
		commands, flags, levels["lossy"], levels["unsupported"], levels["info"])
}
