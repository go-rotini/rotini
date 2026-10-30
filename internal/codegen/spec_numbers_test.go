package codegen

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/toml"
)

// validateSpecDoc runs `rotini validate` on body written as .rotini.spec.<ext> in a fresh module
// and returns the whole report.
func validateSpecDoc(t *testing.T, ext, body string) string {
	t.Helper()
	mod := t.TempDir()
	writeTestFile(t, mod, "go.mod", "module example.com/demo\n\ngo 1.26\n")
	writeTestFile(t, mod, ".rotini.spec."+ext, body)
	writeTestFile(t, mod, ".rotini.conf.yaml", lintFixtureConf)
	t.Chdir(mod)
	var warnings []error
	failure := NewProcessor("0.0.0").Validate(".rotini.spec."+ext, ".rotini.conf.yaml", false, "collect", "",
		func(string, error) {}, func(w []error) { warnings = append(warnings, w...) })
	return lintReport(failure, warnings)
}

const bigIntsJSON = `{
  "version": "0.0.0",
  "command": {
    "name": "demo",
    "flags": [
      {"name": "limit", "identifiers": ["--limit"], "schema": {"type": "int", "default": 1000000}},
      {"name": "big", "identifiers": ["--big"], "schema": {"type": "int64", "default": 9007199254740993}},
      {"name": "level", "identifiers": ["--level"], "schema": {"type": "int", "implicit_value": 1048576}},
      {"name": "sizes", "identifiers": ["--sizes"], "schema": {"type": "[]int", "default": [1000000, 2]}}
    ]
  }
}
`

// TestSpecNumbers_jsonIntegersKeepTheirSpelling pins that JSON and JSONC specs accept integer
// defaults of any size and emit them digit for digit.
func TestSpecNumbers_jsonIntegersKeepTheirSpelling(t *testing.T) {
	for _, ext := range []string{"json", "jsonc"} {
		t.Run(ext, func(t *testing.T) {
			if got := validateSpecDoc(t, ext, bigIntsJSON); got != "" {
				t.Fatalf("validate reported:\n%s", got)
			}
			spec, err := readSpec(".rotini.spec." + ext)
			if err != nil {
				t.Fatal(err)
			}
			flags := spec.Command.inputs().Flags
			for i, want := range []string{"1000000", "9007199254740993"} {
				if got := defaultString(flags[i].Schema.Default); got != want {
					t.Errorf("flag %s default = %q, want %q", flags[i].Name, got, want)
				}
			}
			if got := defaultString(flags[2].Schema.ImplicitValue); got != "1048576" {
				t.Errorf("implicit_value = %q, want 1048576", got)
			}
			if got := defaultList(flags[3].Schema.Default); len(got) != 2 || got[0] != "1000000" {
				t.Errorf("list default = %q, want [1000000 2]", got)
			}
		})
	}
}

// TestSpecNumbers_tomlDatetimeDefault pins that a TOML native datetime is accepted as a time
// default.
func TestSpecNumbers_tomlDatetimeDefault(t *testing.T) {
	const spec = `version = "0.0.0"

[command]
name = "demo"

[[command.flags]]
name = "since"
identifiers = ["--since"]
schema = { type = "time", default = 2026-01-02T03:04:05Z }

[[command.flags]]
name = "day"
identifiers = ["--day"]
schema = { type = "date", default = 2026-01-02 }
`
	if got := validateSpecDoc(t, "toml", spec); got != "" {
		t.Fatalf("validate reported:\n%s", got)
	}
}

// TestDefaultString_everyScalarKind pins the text each decoded scalar kind is emitted as.
func TestDefaultString_everyScalarKind(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		in   any
		want string
	}{
		{float64(1000000), "1000000"},
		{float64(-1048576), "-1048576"},
		{1.5, "1.5"},
		{1e300, "1e+300"},
		{json.Number("18446744073709551615"), "18446744073709551615"},
		{uint64(18446744073709551615), "18446744073709551615"},
		{time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), "2026-01-02T03:04:05Z"},
		{toml.LocalDate{Year: 2026, Month: 1, Day: 2}, "2026-01-02"},
	} {
		if got := defaultString(tt.in); got != tt.want {
			t.Errorf("defaultString(%#v) = %q, want %q", tt.in, got, tt.want)
		}
		if !scalarDefault(tt.in) {
			t.Errorf("scalarDefault(%#v) = false, want true", tt.in)
		}
	}
}

// TestSpecNumbers_nonFiniteIsPositioned pins that an infinite or NaN number in a TOML or YAML
// spec is reported at its key, not as a bare conversion error.
func TestSpecNumbers_nonFiniteIsPositioned(t *testing.T) {
	for _, tt := range []struct{ ext, body, want string }{
		{
			ext:  "toml",
			body: "version = \"0.0.0\"\n\n[command]\nname = \"demo\"\n\n[[command.flags]]\nname = \"x\"\nidentifiers = [\"--x\"]\nschema = { type = \"float64\", default = inf }\n",
			want: ".rotini.spec.toml:9:40:",
		},
		{
			ext:  "yaml",
			body: "version: 0.0.0\ncommand:\n  name: demo\n  flags:\n    - name: x\n      identifiers: [--x]\n      schema: {type: float64, default: .nan}\n",
			want: ".rotini.spec.yaml:7:",
		},
	} {
		t.Run(tt.ext, func(t *testing.T) {
			got := validateSpecDoc(t, tt.ext, tt.body)
			if !strings.HasPrefix(got, tt.want) || !strings.Contains(got, "/command/flags/0/schema/default is infinite or not a number") {
				t.Errorf("report = %q, want it at %s naming the default", got, tt.want)
			}
		})
	}
}
