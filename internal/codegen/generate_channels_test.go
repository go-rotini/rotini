package codegen

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var updateChannels = flag.Bool("update-channels", false, "rewrite testdata/channels/*.golden")

const channelsSpec = `version: 0.0.0
command:
  name: scan
  summary: scan files
  arguments:
    - name: paths
      schema: { type: '[]string' }
  schemas:
    Clash:
      type: object
      required: [message]
      properties:
        message: { type: string }
        key: { type: string }
  channels:
    - name: skipped
      class: info
      summary: a file the scan didn't read
      schema:
        type: object
        required: [path]
        properties:
          path: { type: string }
          reason: { type: string, enum: [binary, too_large] }
    - name: conflict
      class: error
      summary: two files claim one key
      schema:
        type: object
        required: [message, key]
        properties:
          message: { type: string }
          key: { type: string }
    - name: clash
      class: error
      schema: { $ref: Clash }
`

const channelsConf = `version: 0.0.0
generate:
  contract:
    file: cli-contract.json
  packages:
    - type: main
      file: cmd/scan/main.go
    - type: cmd
      file: internal/cmd/scan/zz_rotini.go
      hook_returns: error
`

const channelsHandler = `package scan

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*scanHandler)(nil)

type scanHandler struct{ rotini.NoHooksE }

func (*scanHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[ScanInputs]()
	if err != nil {
		return err
	}
	for _, path := range inputs.Scan.Arguments.Paths {
		switch {
		case strings.HasSuffix(path, ".bin"):
			rtx.Record(SkippedChannel, SkippedRecord{Path: path, Reason: SkippedRecordReasonBinary})
		case strings.HasPrefix(path, "dup"):
			rtx.Record(ConflictChannel, ConflictRecord{Key: path, Message: "two files claim " + path})
		case strings.HasPrefix(path, "clash"):
			rtx.Record(ClashChannel, ClashRecord{Message: "clash on " + path})
		default:
			fmt.Fprintln(rtx.Stdout, "scanned", path)
		}
	}
	return nil
}
`

const channelsMain = `package main

import (
	"os"

	"github.com/go-rotini/rotini"
	cmd "example.com/acme/internal/cmd/scan"
)

func main() {
	cmd.NewProgram().WithReporter(rotini.StructuredReporter(func(*rotini.Context) bool {
		return os.Getenv("SCAN_JSON") != ""
	})).Execute()
}
`

// TestChannels_generatedCode pins what a declared channel generates in the cmd file: a record
// type from its schema, an Error method on an error channel's, its handle, and the
// Definition's ChannelDef list.
func TestChannels_generatedCode(t *testing.T) {
	dir, _ := emitModule(t, channelsSpec, channelsConf)
	src := readEmitted(t, dir, "internal/cmd/scan/zz_rotini.go")
	for _, want := range []string{
		"Channels: []rotini.ChannelDef{\n\t\t{Name: \"skipped\", Class: rotini.ChannelClassInfo},\n\t\t{Name: \"conflict\", Class: rotini.ChannelClassError},\n\t\t{Name: \"clash\", Class: rotini.ChannelClassError},\n\t},",
		"// SkippedRecord is a record of the \"skipped\" channel: a file the scan didn't read.\ntype SkippedRecord struct {\n\tPath   string `json:\"path\"`\n\tReason string `json:\"reason,omitempty\"`\n}",
		"// ClashRecord is a record of the \"clash\" channel.\ntype ClashRecord Clash",
		"func (r ConflictRecord) Error() string { return r.Message }",
		"func (r ClashRecord) Error() string { return r.Message }",
		"SkippedRecordReasonBinary   = \"binary\"",
		"// SkippedChannel is the \"skipped\" channel: a file the scan didn't read.\nvar SkippedChannel = rotini.NewChannel[SkippedRecord](\"skipped\", rotini.ChannelClassInfo)",
		"var ConflictChannel = rotini.NewChannel[ConflictRecord](\"conflict\", rotini.ChannelClassError)",
		"// ClashChannel is the \"clash\" channel. Record into it with rtx.Record.\nvar ClashChannel = rotini.NewChannel[ClashRecord](\"clash\", rotini.ChannelClassError)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("zz_rotini.go lacks\n%s\n---\n%s", want, src)
		}
	}
	if strings.Contains(src, "func (r SkippedRecord) Error") {
		t.Error("an info channel's record got an Error method")
	}
}

// TestChannels_contract checks the contract lists the declared channels, each with its schema
// self-contained, and still matches schema-contract.json.
func TestChannels_contract(t *testing.T) {
	dir, _ := emitModule(t, channelsSpec, channelsConf)
	raw, err := os.ReadFile(filepath.Join(dir, "cli-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema("contract", schemaContractFileBytes)
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateInstance("contract", raw, schema); len(problems) > 0 {
		t.Fatalf("the contract does not match schema-contract.json: %v\n%s", problems, raw)
	}
	var doc struct {
		Channels []struct {
			Name    string         `json:"name"`
			Class   string         `json:"class"`
			Summary string         `json:"summary"`
			Schema  map[string]any `json:"schema"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Channels) != 3 {
		t.Fatalf("channels = %+v, want skipped, conflict, clash", doc.Channels)
	}
	if c := doc.Channels[0]; c.Name != "skipped" || c.Class != "info" || c.Summary != "a file the scan didn't read" || c.Schema["$schema"] != draft07 {
		t.Errorf("channels[0] = %+v", c)
	}
	if c := doc.Channels[2]; c.Schema["$ref"] != "#/definitions/Clash" || c.Schema["definitions"] == nil {
		t.Errorf("channels[2] schema = %v, want the $ref with the definition it reaches", c.Schema)
	}
}

// TestChannels_structuredOutput builds the program and pins its structured report: a declared
// channel's records are lines of their own kind, an error channel's also error lines, and
// every line matches schema-error.json.
func TestChannels_structuredOutput(t *testing.T) {
	skipUnlessCompiling(t)
	golden, err := filepath.Abs(filepath.Join("testdata", "channels", "structured.golden"))
	if err != nil {
		t.Fatal(err)
	}
	dir, _ := emitModule(t, channelsSpec, channelsConf)
	writeTestFile(t, dir, "internal/cmd/scan/scan.go", channelsHandler)
	writeTestFile(t, dir, "cmd/scan/main.go", channelsMain)
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("build:\n%s", out)
	}
	bin := filepath.Join(dir, "scan-bin")
	if runtime.GOOS == "windows" {
		bin += ".exe" // go build adds it, and exec looks for the name as given
	}
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/scan").CombinedOutput(); err != nil {
		t.Fatalf("build:\n%s", out)
	}
	run := func(env ...string) (string, string, int) {
		cmd := exec.Command(bin, "a.txt", "a.bin", "dup1", "clash9")
		cmd.Env = append(os.Environ(), env...)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return stdout.String(), stderr.String(), code
	}

	stdout, stderr, code := run()
	if code != 1 || stdout != "scanned a.txt\n" {
		t.Errorf("text run: code %d, stdout %q", code, stdout)
	}
	if want := "{a.bin binary}\nError: two files claim dup1\nError: clash on clash9\n"; stderr != want {
		t.Errorf("text stderr = %q, want %q", stderr, want)
	}

	_, stderr, code = run("SCAN_JSON=1")
	if code != 1 {
		t.Errorf("structured run exit = %d, want 1", code)
	}
	if *updateChannels {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(stderr), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update-channels)", err)
	}
	if stderr != string(want) {
		t.Errorf("structured stderr =\n%s\nwant\n%s", stderr, want)
	}
	lineSchema, err := compileSchema("error", errorSchemaBytes)
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(stderr) {
		if problems := validateInstance("error", []byte(line), lineSchema); len(problems) > 0 {
			t.Errorf("line %s does not match schema-error.json: %v", line, problems)
		}
	}
}

// TestChannels_modelsTarget compiles the declared channels with the types in a models
// package: the record types and their Error methods live there, and the cmd package's
// handles name them through its aliases.
func TestChannels_modelsTarget(t *testing.T) {
	skipUnlessCompiling(t)
	dir, _ := emitModule(t, channelsSpec, `version: 0.0.0
generate:
  packages:
    - type: cmd
      file: internal/cmd/scan/zz_rotini.go
      hook_returns: error
    - type: models
      file: internal/models/zz_models.go
      package: models
`)
	writeTestFile(t, dir, "internal/cmd/scan/scan.go", channelsHandler)
	if out, err := goBuild(t, dir); err != nil {
		t.Fatalf("the models layout does not compile:\n%s", out)
	}
	models := readEmitted(t, dir, "internal/models/zz_models.go")
	if !strings.Contains(models, "func (r ConflictRecord) Error() string { return r.Message }") {
		t.Errorf("the models file lacks the Error method:\n%s", models)
	}
	cmdFile := readEmitted(t, dir, "internal/cmd/scan/zz_rotini.go")
	if !strings.Contains(cmdFile, "SkippedRecord = models.SkippedRecord") || !strings.Contains(cmdFile, "var SkippedChannel = rotini.NewChannel[SkippedRecord]") {
		t.Errorf("the cmd file lacks the alias or the handle:\n%s", cmdFile)
	}
}

// TestChannels_composedClash refuses a channel name declared by both a parent and the spec it
// composes.
func TestChannels_composedClash(t *testing.T) {
	gp := &program{rootName: "acme", channels: []Channel{{Name: "skipped", Class: "info"}}}
	gp.tree = []rnode{{scope: &schemaScope{name: "scan", channels: []Channel{{Name: "skipped", Class: "warning"}}}}}
	err := gp.composedChannelClash()
	if err == nil || !strings.Contains(err.Error(), `the channel "skipped" is declared by spec "acme" and by spec "scan"`) {
		t.Errorf("err = %v", err)
	}
	same := &schemaScope{name: "scan", channels: []Channel{{Name: "noted", Class: "info"}}}
	gp = &program{rootName: "acme", tree: []rnode{{scope: same}, {scope: same}}}
	if err := gp.composedChannelClash(); err != nil {
		t.Errorf("one spec composed twice: %v", err)
	}
}

// TestDiff_channelRules compares the contracts of spec pairs that change a declared channel.
func TestDiff_channelRules(t *testing.T) {
	skipped := func(props, class string) string {
		return "  channels:\n    - name: skipped\n      class: " + class + "\n      schema:\n        type: object\n        properties:\n" + props
	}
	path := "          path: { type: string }\n"
	reason := "          reason: { type: string }\n"
	runDiffCases(t, []diffCase{
		{name: "removed", old: skipped(path, "info"), new: "", want: []string{"breaking CHANNEL_NO_DELETE channel skipped"}},
		{name: "added", old: "", new: skipped(path, "info"), want: []string{"safe CHANNEL_ADDED channel skipped"}},
		{name: "field removed", old: skipped(path+reason, "info"), new: skipped(path, "info"), want: []string{"breaking OUTPUT_PROPERTY_NO_DELETE channel skipped.reason"}},
		{name: "field added", old: skipped(path, "warning"), new: skipped(path+reason, "warning"), want: []string{"safe OUTPUT_PROPERTY_ADDED channel skipped.reason"}},
		{name: "class", old: skipped(path, "info"), new: skipped(path, "success"), want: []string{"possibly_breaking CHANNEL_CLASS_CHANGED channel skipped"}},
	})
}
