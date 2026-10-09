package rotini

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/go-rotini/jsonschema"
)

// wantsJSON is the program's own rule for a structured run: a --json on the command line.
func wantsJSON(rtx *Context) bool { return slices.Contains(rtx.Argv, "--json") }

func runReporter(structured func(*Context) bool, argv []string, out Outcome, code int) (stdout, stderr string, exit int) {
	rtx := NewContextFor(Definition{Name: "taskr", Commands: []CommandDef{{Name: "list"}}}, argv)
	var o, e bytes.Buffer
	rtx.Stdout, rtx.Stderr = &o, &e
	rtx.exitCode = code
	StructuredReporter(structured)(context.Background(), rtx, out)
	return o.String(), e.String(), rtx.exitCode
}

func TestStructuredReporter_machine(t *testing.T) {
	t.Parallel()
	parse := &ParseError{Kind: ParseKindEnumViolation, Msg: `invalid value "bogus" for --status`, Flag: "--status", Token: "bogus"}
	out := Outcome{
		Infos:     []string{"reading tasks"},
		Warnings:  []error{errors.New("the cache is stale")},
		Errors:    []error{parse, InternalError(errors.New("the store is gone"))},
		Panics:    []*PanicError{{Value: "boom"}},
		Successes: []string{"done"},
	}
	stdout, stderr, code := runReporter(wantsJSON, []string{"list", "--json"}, out, 0)
	if stdout != "" {
		t.Errorf("stdout was touched: %q", stdout)
	}
	if code != 1 {
		t.Errorf("exit = %d, want the default reporter's floor of 1", code)
	}
	want := []string{
		`{"info":{"command":"taskr list","message":"reading tasks"}}`,
		`{"warning":{"command":"taskr list","message":"the cache is stale"}}`,
		`{"error":{"category":"usage","command":"taskr list","exit_code":1,"flag":"--status","kind":"enum-violation","message":"invalid value \"bogus\" for --status","token":"bogus"}}`,
		`{"error":{"category":"internal","command":"taskr list","exit_code":1,"message":"the store is gone"}}`,
		`{"error":{"category":"internal","command":"taskr list","exit_code":1,"message":"boom"}}`,
		`{"success":{"command":"taskr list","message":"done"}}`,
	}
	got := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("stderr =\n%s\nwant\n%s", stderr, strings.Join(want, "\n"))
	}

	raw, err := os.ReadFile("schema-error.json")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := jsonschema.Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range got {
		res, err := schema.Validate([]byte(line))
		if err != nil || !res.Valid {
			t.Errorf("line does not match schema-error.json: %s (%v %+v)", line, err, res)
		}
	}
}

func TestStructuredReporter_keepsADeliberateCode(t *testing.T) {
	t.Parallel()
	_, stderr, code := runReporter(wantsJSON, []string{"list", "--json"}, Outcome{Errors: []error{errors.New("partial")}}, 3)
	if code != 3 || !strings.Contains(stderr, `"exit_code":3`) {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestStructuredReporter_text(t *testing.T) {
	t.Parallel()
	out := Outcome{Infos: []string{"hello"}, Errors: []error{errors.New("nope")}}
	for name, tc := range map[string]struct {
		structured func(*Context) bool
		argv       []string
	}{
		"the program says no":  {wantsJSON, []string{"list"}},
		"no rule at all (nil)": {nil, []string{"list", "--json"}},
		"no command at all":    {wantsJSON, nil},
	} {
		stdout, stderr, code := runReporter(tc.structured, tc.argv, out, 0)
		if stdout != "" || stderr != "hello\nError: nope\n" || code != 1 {
			t.Errorf("%s: stdout %q, stderr %q, exit %d; want the default reporter's text", name, stdout, stderr, code)
		}
	}
}
