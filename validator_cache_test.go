package rotini

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rotini/recon"
)

func TestValidatorCache_compilesOnce(t *testing.T) {
	const schema = `{"type":"object","required":["cache-probe"]}`
	a, err := schemaValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := schemaValidator(schema); a.schema != b.schema {
		t.Error("schemaValidator compiled the same schema twice")
	}
	if _, err := schemaValidator(`{"type":`); err == nil {
		t.Error("schemaValidator accepted an invalid schema")
	}

	re, err := compiledPattern(`^cache-probe-[0-9]+$`)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := compiledPattern(`^cache-probe-[0-9]+$`); re != again {
		t.Error("compiledPattern compiled the same pattern twice")
	}
	if _, err := compiledPattern(`(`); err == nil {
		t.Error("compiledPattern accepted an invalid pattern")
	}
}

// A value that matches no branch of an anyOf or oneOf is one failure that names the branches as
// alternatives, each wording once; a failure beneath another keyword is still reported itself.
func TestSchemaFailures_noBranchMatches(t *testing.T) {
	for _, tc := range []struct {
		schema string
		doc    map[string]any
		want   string
	}{
		{`{"properties":{"v":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}`, map[string]any{"v": true},
			"recon: validation v [oneOf]: matches none of: value is not of type string or value is not of type integer"},
		{`{"properties":{"v":{"anyOf":[{"const":"a"},{"const":"b"}]}}}`, map[string]any{"v": "c"},
			"recon: validation v [anyOf]: value does not match const"},
		{`{"properties":{"v":{"anyOf":[{"type":"string","minLength":5,"pattern":"^x"},{"type":"integer"}]}}}`, map[string]any{"v": "ab"},
			"recon: validation v [anyOf]: matches none of: string length 2 is below minLength 5 and value does not match pattern \"^x\" or value is not of type integer"},
		{`{"properties":{"v":{"anyOf":[{"properties":{"a":{"type":"string"}}},{"type":"integer"}]}}}`, map[string]any{"v": map[string]any{"a": 1}},
			"recon: 2 errors:\n  - recon: validation v.a [type]: value is not of type string\n  - recon: validation v [type]: value is not of type integer"},
	} {
		v, err := schemaValidator(tc.schema)
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Validate(tc.doc); got == nil || got.Error() != tc.want {
			t.Errorf("%s: %v, want %q", tc.schema, got, tc.want)
		}
	}
}

// An unsigned integer a document decodes to, up to the uint64 maximum, is a number to the
// schema check, and the document itself is left as it was.
func TestSchemaCheck_unsignedIntegers(t *testing.T) {
	v, err := schemaValidator(`{"properties":{"n":{"type":"integer","minimum":0},"l":{"items":{"type":"integer"}}}}`)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{"n": uint64(math.MaxUint64), "l": []any{uint8(1), uint64(1) << 63}}
	if err := v.Validate(doc); err != nil {
		t.Errorf("Validate = %v, want the unsigned integers accepted", err)
	}
	if _, ok := doc["n"].(uint64); !ok {
		t.Errorf("the document was changed: %#v", doc)
	}
	if err := v.Validate(map[string]any{"n": "18446744073709551615"}); err == nil {
		t.Error("a string passed type: integer")
	}
}

// A TOML date-time, local date-time, local date and local time is text to a schema check, as
// the same value written in YAML or JSON is, through a configuration file and a stdin document.
func TestSchemaCheck_timesAreText(t *testing.T) {
	const schema = `{"type":"object","properties":{"dt":{"type":"string"},"ldt":{"type":"string"},"ld":{"type":"string","format":"date"},"lt":{"type":"string"}}}`
	const doc = "dt = 2024-01-02T03:04:05Z\nldt = 2024-01-02T03:04:05\nld = 2024-01-02\nlt = 03:04:05\n"
	cfg := filepath.Join(t.TempDir(), "app.toml")
	if err := os.WriteFile(cfg, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	var in struct {
		App struct {
			Flags     struct{}
			Arguments struct{}
			Config    struct {
				Dt time.Time `rotini:"dt" recon:"dt"`
			}
		}
	}
	err := NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Schema: schema}}}).
		Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
	if err != nil || !in.App.Config.Dt.Equal(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("config file: dt %v, err %v", in.App.Config.Dt, err)
	}
	m, err := recon.TOML.Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	v, err := schemaValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(m); err != nil {
		t.Errorf("decoded TOML document: %v", err)
	}
	if _, ok := m["dt"].(time.Time); !ok {
		t.Errorf("the document was changed: %#v", m["dt"])
	}
}

// A schema check that stops before it finishes, such as at jsonschema's evaluation limit, reports
// the configuration file as not fully checked, never as invalid.
func TestSchemaCheck_unfinishedIsNotInvalid(t *testing.T) {
	schema := unfinishedSchema()
	v, err := schemaValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(map[string]any{}); err == nil {
		t.Skip("this jsonschema finishes the check: it has no evaluation limit")
	}
	cfg := filepath.Join(t.TempDir(), "app.json")
	if err := os.WriteFile(cfg, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var in struct {
		App struct {
			Flags, Arguments struct{}
			Config           struct {
				X string `rotini:"x" recon:"x"`
			}
		}
	}
	err = NewInputReader(InputSettings{ConfigFiles: []ConfigFile{{Name: "app", Path: cfg, Schema: schema}}}).
		Read(NewContextFor(Definition{Name: "app", Handler: "App"}, nil), &in)
	if err == nil || !strings.Contains(err.Error(), "could not be fully checked against its schema") || strings.Contains(err.Error(), "invalid") {
		t.Errorf("err = %v, want the file reported as not fully checked", err)
	}
}

// unfinishedSchema returns a schema whose $ref chains double at each step, which a jsonschema
// with an evaluation limit stops checking before it finishes.
func unfinishedSchema() string {
	var b strings.Builder
	b.WriteString(`{"$ref":"#/$defs/d0","$defs":{`)
	for i := range 21 {
		fmt.Fprintf(&b, `"d%d":{"allOf":[{"$ref":"#/$defs/d%d"},{"$ref":"#/$defs/d%d"}]},`, i, i+1, i+1)
	}
	b.WriteString(`"d21":{"type":"object"}}}`)
	return b.String()
}

// An object flag and a stdin document whose schema check stops before it finishes are reported
// as not fully checked, never as a mismatch or a read failure.
func TestSchemaCheck_unfinishedObjectAndStdin(t *testing.T) {
	schema := unfinishedSchema()
	v, err := schemaValidator(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Validate(map[string]any{}); err == nil {
		t.Skip("this jsonschema finishes the check: it has no evaluation limit")
	}
	if err := validateObject(map[string]any{"a": 1}, schema); err == nil || err.Error() != "the value could not be fully checked against its schema" {
		t.Errorf("object flag: %v", err)
	}
	_, err = ssRead[stDocInputs](t, strings.NewReader(`{"name":"a"}`), InputSettings{StdinSchemas: map[string]string{"stDocStdin": schema}})
	if err == nil || err.Error() != stdinUnfinished {
		t.Errorf("stdin document: %v", err)
	}
}

func TestSchemaVerdict(t *testing.T) {
	if got := schemaVerdict(fmt.Errorf("x: %w", errSchemaUnfinished), "is invalid"); got != "could not be fully checked against its schema" {
		t.Errorf("unfinished: %q", got)
	}
	if got := schemaVerdict(errors.New("x"), "is invalid"); got != "is invalid: does not match its schema" {
		t.Errorf("failed: %q", got)
	}
}
