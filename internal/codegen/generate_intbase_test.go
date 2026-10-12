package codegen

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const intBaseSpec = `version: 0.0.0
command:
  name: app
  summary: an integer base fixture
  flags:
    - name: mode
      summary: file mode
      identifiers: [--mode]
      schema: { type: uint32, base: prefixed, default: '0o644', maximum: 511 }
    - name: mask
      summary: masks
      identifiers: [--mask]
      schema: { type: '[]uint8', base: prefixed }
  arguments:
    - name: n
      summary: a number
      schema: { type: int, base: prefixed }
  env:
    - name: port
      summary: the port
      schema: { type: int, base: prefixed, default: '010' }
`

func intBaseProgram(t *testing.T) *program {
	t.Helper()
	gp, err := resolveTree(decodeSpecYAML(t, intBaseSpec), filepath.Join(t.TempDir(), ".rotini.spec.yaml"), "example.com/app")
	if err != nil {
		t.Fatal(err)
	}
	return gp
}

// The Definition carries the base on flags and arguments, and env fields carry it as a tag,
// with a default the recon tag spells in decimal, since recon reads a leading 0 as octal.
func TestIntBase_literalsAndTags(t *testing.T) {
	gp := intBaseProgram(t)
	flags := flagDefsLiteral(gp.rootInputs, nil, false)
	for _, want := range []string{`Default: "0o644", Base: "prefixed"`, `Type: "[]uint8", Base: "prefixed"`} {
		if !strings.Contains(flags, want) {
			t.Errorf("flag literal is missing %s:\n%s", want, flags)
		}
	}
	if args := argDefsLiteral(gp.rootInputs, nil); !strings.Contains(args, `Base: "prefixed"`) {
		t.Errorf("argument literal is missing the base:\n%s", args)
	}
	env := gp.inputFieldsOf(gp.rootInputs).env[0]
	if env.Recon != "port,default=10" || !strings.Contains(env.Constraint, `base:"prefixed"`) {
		t.Errorf("env field: recon %q, tags %q", env.Recon, env.Constraint)
	}
	var key strings.Builder
	writeKeyRules(&key, gp.rootInputs.Env[0].Schema)
	if got := key.String(); got != `, Base: "prefixed"` {
		t.Errorf("config key rules %q", got)
	}
}

// Help notes the prefixes an integer input accepts.
func TestIntBase_helpNote(t *testing.T) {
	gp := intBaseProgram(t)
	var data templateHelpData
	for _, n := range flattenFeature(gp, helpFeatureDesc) {
		if len(n.path) == 0 {
			data = n.data
		}
	}
	data.Headings = resolveHeadings(cmdHelp{})
	tmpl, err := parseDocTemplate("help", templateHelp)
	if err != nil {
		t.Fatal(err)
	}
	help, err := renderDocText(tmpl, data)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(help, "(accepts 0x, 0o and 0b prefixes)"); got != 4 {
		t.Errorf("help notes the prefixes %d times, want 4 (two flags, the argument, the env input)\n%s", got, help)
	}
}

// The contract states the base, and each input's JSON Schema takes a prefixed string as well
// as an integer, a list's items included.
func TestIntBase_contract(t *testing.T) {
	gp := intBaseProgram(t)
	doc, err := gp.contract(gp.contractNodes())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Commands []struct {
			Flags []struct {
				Name   string         `json:"name"`
				Base   string         `json:"base"`
				Schema map[string]any `json:"schema"`
			} `json:"flags"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	flags := parsed.Commands[0].Flags
	mode, mask := flags[0], flags[1]
	if mode.Base != "prefixed" || mask.Base != "prefixed" {
		t.Errorf("base %q, %q", mode.Base, mask.Base)
	}
	isPrefixed := func(s map[string]any) bool {
		types, _ := s["type"].([]any)
		return slices.Equal(types, []any{"integer", "string"}) && s["pattern"] == prefixedPattern
	}
	if !isPrefixed(mode.Schema) || mode.Schema["maximum"] != float64(511) || mode.Schema["default"] != "0o644" {
		t.Errorf("mode schema %v", mode.Schema)
	}
	items, _ := mask.Schema["items"].(map[string]any)
	if mask.Schema["type"] != "array" || !isPrefixed(items) {
		t.Errorf("mask schema %v", mask.Schema)
	}
}

// prefixedText reads a literal the way the runtime does.
func TestIntBase_prefixedText(t *testing.T) {
	for in, want := range map[string]string{
		"0x1F": "31", "0X1f": "31", "0o17": "15", "0b101": "5", "-0x10": "-16", "+0b1": "1", "010": "10",
		"1_000": "1000", "0xFF_FF": "65535", "0x_1F": "31",
		"1__0": "1__0", "_1": "_1", "1_": "1_", "-_1": "-_1", "0x": "0x", "abc": "abc", "": "",
	} {
		if got := prefixedText(in); got != want {
			t.Errorf("prefixedText(%q) = %q, want %q", in, got, want)
		}
	}
}
