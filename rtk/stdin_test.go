package rtk_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/rtk"
)

// =============================================================================
// End-to-end stdin shaping via NewParser + Parse — root command
// =============================================================================

func TestStdin_text_root(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "text"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader("hello world\n"),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.(string)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want string", tgt.result.Stdin, tgt.result.Stdin)
	}
	if got != "hello world" {
		t.Errorf("text stdin: got %q, want %q", got, "hello world")
	}
}

func TestStdin_text_trimsTrailingCRLF(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "text"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	for _, ending := range []string{"\n", "\r\n", "\r\n\r\n", "\n\r\n"} {
		t.Run(ending, func(t *testing.T) {
			p := rtk.NewParser(spec, rtk.Inputs{
				Argv:  []string{"--help"},
				Stdin: strings.NewReader("multi\nline" + ending),
			})
			tgt := &asTarget{}
			if err := p.Parse(tgt); err != nil {
				t.Fatalf("Parse error: %v", err)
			}
			if got, _ := tgt.result.Stdin.(string); got != "multi\nline" {
				t.Errorf("trim trailing: got %q, want %q", got, "multi\nline")
			}
		})
	}
}

func TestStdin_raw_root(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "raw"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0xFF}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(string(payload)),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.([]byte)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want []byte", tgt.result.Stdin, tgt.result.Stdin)
	}
	if !reflect.DeepEqual(got, payload) {
		t.Errorf("raw stdin: got %v, want %v", got, payload)
	}
}

func TestStdin_json_root(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "json"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(`{"name":"rotini","count":42,"on":true}`),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.(map[string]any)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want map[string]any", tgt.result.Stdin, tgt.result.Stdin)
	}
	if got["name"] != "rotini" {
		t.Errorf("name: got %v, want \"rotini\"", got["name"])
	}
	// JSON numbers decode to float64
	if got["count"] != float64(42) {
		t.Errorf("count: got %v (%T), want 42.0", got["count"], got["count"])
	}
	if got["on"] != true {
		t.Errorf("on: got %v, want true", got["on"])
	}
}

func TestStdin_json_malformed_returnsError(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "json"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(`{not json}`),
	})
	err := p.Parse(&asTarget{})
	if err == nil {
		t.Fatal("expected error for malformed JSON stdin")
	}
	if !strings.Contains(err.Error(), "stdin (json)") {
		t.Errorf("error message: got %q, want contains \"stdin (json)\"", err.Error())
	}
}

func TestStdin_yaml_root(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "yaml"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	yaml := "name: rotini\ncount: 42\non: true\n"
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(yaml),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.(map[string]any)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want map[string]any", tgt.result.Stdin, tgt.result.Stdin)
	}
	if got["name"] != "rotini" {
		t.Errorf("name: got %v, want \"rotini\"", got["name"])
	}
	if got["count"] == nil {
		t.Errorf("count: got nil, want 42")
	}
	if got["on"] != true {
		t.Errorf("on: got %v, want true", got["on"])
	}
}

func TestStdin_toml_root(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "toml"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	tomlPayload := "name = \"rotini\"\ncount = 42\non = true\n"
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(tomlPayload),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.(map[string]any)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want map[string]any", tgt.result.Stdin, tgt.result.Stdin)
	}
	if got["name"] != "rotini" {
		t.Errorf("name: got %v, want \"rotini\"", got["name"])
	}
}

func TestStdin_jsonc_root(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "jsonc"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	// JSONC tolerates comments and trailing commas.
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv: []string{"--help"},
		Stdin: strings.NewReader(`{
			// a comment
			"name": "rotini",
			"count": 42, // trailing comma below
		}`),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.(map[string]any)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want map[string]any", tgt.result.Stdin, tgt.result.Stdin)
	}
	if got["name"] != "rotini" {
		t.Errorf("name: got %v, want \"rotini\"", got["name"])
	}
}

// =============================================================================
// Edge cases
// =============================================================================

func TestStdin_emptyStdin_noShape(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "json"}, // expects structured, but stdin is empty
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(""),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if tgt.result.Stdin != nil {
		t.Errorf("empty stdin: Result.Stdin should stay nil, got %v", tgt.result.Stdin)
	}
}

func TestStdin_nilReader_noShape(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "text"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: nil, // nil reader (most common: a non-piped stdin TTY check would have returned nil)
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if tgt.result.Stdin != nil {
		t.Errorf("nil stdin: Result.Stdin should stay nil, got %v", tgt.result.Stdin)
	}
}

func TestStdin_noSpec_noShape(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		// No RootStdin and no per-command Stdin
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader("some content that nothing should consume"),
	})
	tgt := &asTarget{}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if tgt.result.Stdin != nil {
		t.Errorf("no StdinSpec: Result.Stdin should stay nil, got %v", tgt.result.Stdin)
	}
}

func TestStdin_unknownFormat_returnsError(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "bogus"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader("hello"),
	})
	err := p.Parse(&asTarget{})
	if err == nil {
		t.Fatal("expected error for unknown stdin format")
	}
	if !strings.Contains(err.Error(), "unknown stdin format") {
		t.Errorf("error message: got %q, want contains \"unknown stdin format\"", err.Error())
	}
}

// =============================================================================
// Subcommand stdin shaping
// =============================================================================

func TestStdin_subcommand_shapeOnLeaf(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "send", Path: "send",
				Stdin: &rtk.StdinSpec{Format: "json"},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"send"},
		Stdin: strings.NewReader(`{"to":"user@example.com","subject":"hi"}`),
	})
	tgt := &asTarget{path: "send"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	got, ok := tgt.result.Stdin.(map[string]any)
	if !ok {
		t.Fatalf("Result.Stdin: got %v (%T), want map[string]any", tgt.result.Stdin, tgt.result.Stdin)
	}
	if got["to"] != "user@example.com" {
		t.Errorf("to: got %v, want user@example.com", got["to"])
	}
}

func TestStdin_subcommand_noSpec_noShape(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{Name: "send", Path: "send"}, // no Stdin
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"send"},
		Stdin: strings.NewReader("payload ignored"),
	})
	tgt := &asTarget{path: "send"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if tgt.result.Stdin != nil {
		t.Errorf("no StdinSpec on subcommand: got %v", tgt.result.Stdin)
	}
}

// =============================================================================
// `-` substitution still works (M1.4 carry-over)
// =============================================================================

func TestStdin_dashSubstitution_intoPositional(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "echo", Path: "echo",
				Arguments: []rtk.ArgumentSpec{
					{Name: "msg", Type: "string"},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"echo", "-"},
		Stdin: strings.NewReader("piped content"),
	})
	tgt := &asTarget{path: "echo"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if len(tgt.result.ParsedArgs) != 1 || tgt.result.ParsedArgs[0] != "piped content" {
		t.Errorf("ParsedArgs: got %v, want [\"piped content\"]", tgt.result.ParsedArgs)
	}
}

func TestStdin_dashSubstitution_intoFlagValue(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "send", Path: "send",
				Flags: []rtk.FlagSpec{
					{Name: "body", Identifiers: []string{"-b"}, Type: "string"},
				},
			},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"send", "-b", "-"},
		Stdin: strings.NewReader("the message body"),
	})
	tgt := &asTarget{path: "send"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	v, _ := unwrapSingleAny(tgt.result.FlagsByScope["send"]["body"])
	if v != "the message body" {
		t.Errorf("flag body: got %v, want \"the message body\"", v)
	}
}

// =============================================================================
// Single-read guarantee — stdin is read once, exposed to both `-` substitution
// and shaping without re-reading from the io.Reader.
// =============================================================================

// readCounter wraps a strings.Reader and counts how many times Read is called.
// The parser should call Read until EOF once and never re-read.
type readCounter struct {
	src   *strings.Reader
	calls int
}

func (r *readCounter) Read(p []byte) (int, error) {
	r.calls++
	return r.src.Read(p)
}

func TestStdin_singleReadAcrossBothUses(t *testing.T) {
	t.Parallel()

	// A spec that uses stdin both for `-` substitution AND for shaping.
	spec := rtk.ProgramSpec{
		Commands: []rtk.CommandSpec{
			{
				Name: "send", Path: "send",
				Stdin: &rtk.StdinSpec{Format: "text"},
				Arguments: []rtk.ArgumentSpec{
					{Name: "to", Type: "string"},
				},
			},
		},
	}
	rc := &readCounter{src: strings.NewReader("the message body\n")}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"send", "-"},
		Stdin: rc,
	})
	tgt := &asTarget{path: "send"}
	if err := p.Parse(tgt); err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	if rc.calls == 0 {
		t.Error("stdin never read")
	}
	// The reader is drained by io.ReadAll in one logical read pass — it may
	// take multiple Read calls (buffer-sized chunks + final EOF), but reads
	// happen ONCE and the result is cached. Subsequent uses of stdin
	// (positional substitution + shaping) draw from the cache.
	//
	// We can't check exact call count (depends on io.ReadAll buffering), but
	// we can verify both consumers got the data:
	if len(tgt.result.ParsedArgs) != 1 || tgt.result.ParsedArgs[0] != "the message body\n" {
		t.Errorf("ParsedArgs: got %v, want [\"the message body\\n\"]", tgt.result.ParsedArgs)
	}
	if got, _ := tgt.result.Stdin.(string); got != "the message body" {
		t.Errorf("Result.Stdin: got %q, want %q", got, "the message body")
	}
}

// =============================================================================
// Sanity: errors don't leak the wrong sentinel
// =============================================================================

func TestStdin_jsonError_unwrapsToOriginalError(t *testing.T) {
	t.Parallel()

	spec := rtk.ProgramSpec{
		RootStdin: &rtk.StdinSpec{Format: "json"},
		Flags: []rtk.FlagSpec{
			{Name: "help", Identifiers: []string{"--help"}, Type: "bool"},
		},
	}
	p := rtk.NewParser(spec, rtk.Inputs{
		Argv:  []string{"--help"},
		Stdin: strings.NewReader(`{not json`),
	})
	err := p.Parse(&asTarget{})
	if err == nil {
		t.Fatal("expected error")
	}
	// errors.Is against the json package's syntax errors isn't a stable
	// promise (encoding/json doesn't export sentinel errors), but the
	// wrapping should preserve the underlying error in the chain.
	if errors.Unwrap(err) == nil {
		t.Error("error should wrap the underlying JSON parser error")
	}
}
