package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStdin_streamAndNewFormats pins the generated Stdin field for each new shape, the stdin
// tag's options, and the iter import a stream needs.
func TestStdin_streamAndNewFormats(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: demo
  schemas:
    Task:
      type: object
      properties:
        name: {type: string}
  commands:
    - name: grep
      arguments:
        - name: files
          schema: {type: '[]existingfile'}
      stdin: {format: lines, stream: true, separator: nul, unless_argument: files, schema: {required: true}}
    - name: tasks
      stdin: {format: jsonl, stream: true, schema: {$ref: '#/schemas/Task'}}
    - name: load
      stdin: {format: jsonl, schema: {$ref: '#/schemas/Task'}}
    - name: hash
      stdin: {format: bytes}
`
	gen := emitInModule(t, spec, goldenConf)["internal/cmd/demo/zz_demo.go"]
	for _, want := range []string{
		"Stdin     iter.Seq2[string, error] `stdin:\"lines,stream,nul,required,unless=files\"`",
		"Stdin     iter.Seq2[DemoTasksStdin, error] `stdin:\"jsonl,stream\"`",
		"Stdin     *[]DemoLoadStdin `stdin:\"jsonl\"`",
		"Stdin     *[]byte `stdin:\"bytes\"`",
		"\t\"iter\"\n",
		`"DemoTasksStdin":`,
		`"DemoLoadStdin":`,
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
	if strings.Contains(gen, `"DemoHashStdin"`) || strings.Contains(gen, "type DemoHashStdin") {
		t.Error("bytes generated a <Prefix>Stdin type or schema")
	}
}

// TestDataInputs_generated pins the envfile tag, a NUL separator in the FlagDef, and a config
// file read as environment in InputSettings.
func TestDataInputs_generated(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: demo
  config_files:
    - name: dotenv
      path: .env
      format: dotenv
      as: env
  flags:
    - name: token
      identifiers: [--token]
      schema: {type: string, variable: DEMO_TOKEN, variable_file: DEMO_TOKEN_FILE}
    - name: files-from
      identifiers: [--files-from]
      schema: {type: '[]string', separator: nul, from: [file, stdin]}
  env:
    - name: password
      schema: {type: string, variable_file: DEMO_PASSWORD_FILE}
`
	gen := emitInModule(t, spec, goldenConf)["internal/cmd/demo/zz_demo.go"]
	for _, want := range []string{
		`env:"DEMO_TOKEN" envfile:"DEMO_TOKEN_FILE"`,
		`env:"PASSWORD" envfile:"DEMO_PASSWORD_FILE"`,
		`Separator: "\x00"`,
		`As: "env"`,
	} {
		if !strings.Contains(gen, want) {
			t.Errorf("generated file is missing %q", want)
		}
	}
}

// TestStdin_pagesSayHowStdinIsRead pins the stdin wording of the help, man and markdown pages.
func TestStdin_pagesSayHowStdinIsRead(t *testing.T) {
	spec := `version: 0.0.0
command:
  name: demo
  commands:
    - name: grep
      arguments:
        - name: files
          schema: {type: '[]existingfile', placeholder: FILE}
      stdin: {format: lines, stream: true, separator: nul, unless_argument: files}
`
	dir, _ := emitModule(t, spec, goldenConf+`  features:
    - type: help
      enabled: true
      embed: true
    - type: man
      enabled: true
      embed: true
    - type: markdown
      enabled: true
      embed: true
`)
	var all strings.Builder
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		all.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"lines []string (NUL-separated) (streamed) (only when no <FILE> is given)",
		"Reads lines of text separated by NUL bytes from standard input, one at a time,\nwhen no <FILE> is given.",
		"Reads lines of text separated by NUL bytes from stdin, one at a time, when no `<FILE>` is given.",
	} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("pages are missing %q", want)
		}
	}
}
