package internal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	validSpecHeader = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json\n"
	validConfHeader = "$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-conf.json\n"
)

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestValidate_validSpec(t *testing.T) {
	path := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	if err := validateOnce(path, "", ""); err != nil {
		t.Errorf("Validate(valid spec) = %v, want nil", err)
	}
}

func TestValidate_validSpecAndConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader)
	if err := validateOnce(spec, conf, ""); err != nil {
		t.Errorf("Validate(valid spec+conf) = %v, want nil", err)
	}
}

// TestValidate_singlePassRouting exercises the public Validate orchestrator (watch=false),
// which mirrors Generate: a valid pass hands a "[HH:MM:SS] <took>" summary to onValidate and
// returns nil; a failing pass returns the error and does NOT route it through the callback.
func TestValidate_singlePassRouting(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	var result string
	var cbErr error
	called := 0
	if err := Validate(spec, "", false, "", func(r string, e error) { called++; result, cbErr = r, e }); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}
	if called != 1 || cbErr != nil || result == "" {
		t.Errorf("valid pass: called=%d result=%q err=%v, want one call with a summary and nil err", called, result, cbErr)
	}

	called = 0
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if err := Validate(missing, "", false, "", func(string, error) { called++ }); err == nil {
		t.Error("Validate(missing spec) = nil, want an error")
	}
	if called != 0 {
		t.Errorf("callback called %d times on a non-watch failure, want 0 (the error is returned)", called)
	}
}

func TestValidate_missingRequiredField(t *testing.T) {
	// Required "command" is absent at the document level.
	path := writeTemp(t, "spec.yaml", validSpecHeader)
	err := validateOnce(path, "", "")
	if err == nil {
		t.Fatal("expected error for spec missing required command")
	}
	if !strings.Contains(err.Error(), "command") {
		t.Errorf("error does not mention the missing field: %v", err)
	}
}

func TestValidate_unknownField(t *testing.T) {
	// additionalProperties:false must reject unknown fields; this only
	// works because validation runs on the raw instance, not a decoded
	// struct (which would silently drop "bogus").
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","bogus":true}}`
	path := writeTemp(t, "spec.json", doc)
	if err := validateOnce(path, "", ""); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestValidate_helpKeys(t *testing.T) {
	// Flattened help fields on the root and on a command, plus input summaries
	// and hidden/deprecated, all validate.
	spec := validSpecHeader +
		"command:\n" +
		"  name: demo\n" +
		"  summary: a demo\n" +
		"  description: A demo CLI.\n" +
		"  usage: demo [flags]\n" +
		"  headings:\n" +
		"    commands: Subcommands\n" +
		"  examples: [demo run]\n" +
		"  commands:\n" +
		"    - name: run\n" +
		"      summary: run it\n" +
		"      hidden: true\n" +
		"      deprecated: use start\n" +
		"      inputs:\n" +
		"        flags:\n" +
		"          - name: force\n" +
		"            summary: force it\n" +
		"            hidden: true\n" +
		"            deprecated: no longer needed\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := validateOnce(path, "", ""); err != nil {
		t.Errorf("Validate(spec with help keys) = %v, want nil", err)
	}
}

func TestValidate_localTimeoutRejected(t *testing.T) {
	// timeout on a (local) sub-command is a remote-only concept — validation rejects
	// it (and the walk reaches nested commands).
	spec := validSpecHeader +
		"command:\n" +
		"  name: demo\n" +
		"  commands:\n" +
		"    - name: run\n" +
		"      timeout: 5s\n"
	path := writeTemp(t, "spec.yaml", spec)
	err := validateOnce(path, "", "")
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "remote") {
		t.Errorf("Validate(local timeout) = %v, want a timeout/remote rejection", err)
	}
}

func TestValidate_remoteTimeoutAccepted(t *testing.T) {
	// timeout on a remote_commands entry (host-side) is valid.
	spec := validSpecHeader +
		"command:\n" +
		"  name: demo\n" +
		"  remote_commands:\n" +
		"    - name: plugin\n" +
		"      timeout: 10s\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := validateOnce(path, "", ""); err != nil {
		t.Errorf("Validate(remote_commands timeout) = %v, want nil", err)
	}
}

func TestValidate_flagGroupUnknownFlag(t *testing.T) {
	// A flag_groups entry referencing a flag the command doesn't declare is rejected.
	spec := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: json\n" +
		"        schema: { type: bool }\n" +
		"    flag_groups:\n" +
		"      - kind: mutually_exclusive\n" +
		"        flags: [json, nope]\n"
	path := writeTemp(t, "spec.yaml", spec)
	err := validateOnce(path, "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Validate(flag group with unknown flag) = %v, want an unknown-flag error", err)
	}
}

func TestValidate_danglingSchemaRef(t *testing.T) {
	// A $ref to a schema the document doesn't declare is rejected (it would otherwise be
	// an "undefined type" compile error in the generated code), with a suggestion.
	spec := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    config:\n" +
		"      - name: server\n" +
		"        schema: { $ref: '#/schemas/Endpiont' }\n" +
		"schemas:\n" +
		"  Endpoint:\n" +
		"    type: object\n" +
		"    properties:\n" +
		"      host: { type: string }\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "")
	if err == nil || !strings.Contains(err.Error(), "undeclared schema") || !strings.Contains(err.Error(), `did you mean "Endpoint"`) {
		t.Errorf("Validate(dangling $ref) = %v, want an undeclared-schema error suggesting Endpoint", err)
	}

	// A valid $ref to a declared schema passes.
	ok := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    config:\n" +
		"      - name: server\n" +
		"        schema: { $ref: '#/schemas/Endpoint' }\n" +
		"schemas:\n" +
		"  Endpoint:\n" +
		"    type: object\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", ""); err != nil {
		t.Errorf("Validate(valid $ref) = %v, want nil", err)
	}
}

func TestValidate_flagGroupSuggestion(t *testing.T) {
	// A flag_groups reference that's a near-typo of a real flag gets a "did you mean".
	spec := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: json\n" +
		"        schema: { type: bool }\n" +
		"    flag_groups:\n" +
		"      - kind: mutually_exclusive\n" +
		"        flags: [json, jsno]\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "")
	if err == nil || !strings.Contains(err.Error(), `did you mean "json"`) {
		t.Errorf("Validate(flag group typo) = %v, want a did-you-mean suggestion", err)
	}
}

func TestValidate_flagDependencyUnknownFlag(t *testing.T) {
	// A flag_dependencies entry whose 'requires' names a flag the command doesn't
	// declare is rejected (the 'when' flag is checked the same way).
	spec := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: tls\n" +
		"        schema: { type: bool }\n" +
		"    flag_dependencies:\n" +
		"      - when: tls\n" +
		"        requires: [cert]\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "cert") {
		t.Errorf("Validate(flag dependency requiring unknown flag) = %v, want an unknown-flag error", err)
	}
}

func TestValidate_duplicateFlagIdentifier(t *testing.T) {
	// Two flags claiming the same explicit identifier (-o) on one command is rejected.
	spec := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: output\n" +
		"        identifiers: [-o, --output]\n" +
		"        schema: { type: string }\n" +
		"      - name: organization\n" +
		"        identifiers: [-o, --org]\n" +
		"        schema: { type: string }\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "")
	if err == nil || !strings.Contains(err.Error(), `"-o"`) || !strings.Contains(err.Error(), "output") {
		t.Errorf("Validate(duplicate -o) = %v, want a duplicate-identifier error naming -o and output", err)
	}

	// Auto-derived collision: two flags whose names both yield --out (no identifiers).
	spec2 := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: out\n" +
		"        schema: { type: string }\n" +
		"      - name: out\n" +
		"        schema: { type: bool }\n"
	if err := validateOnce(writeTemp(t, "spec2.yaml", spec2), "", ""); err == nil || !strings.Contains(err.Error(), "--out") {
		t.Errorf("Validate(derived --out collision) = %v, want a duplicate-identifier error", err)
	}

	// Distinct identifiers validate cleanly.
	ok := validSpecHeader +
		"command:\n" +
		"  name: app\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: output\n" +
		"        identifiers: [-o, --output]\n" +
		"        schema: { type: string }\n" +
		"      - name: verbose\n" +
		"        identifiers: [-v, --verbose]\n" +
		"        schema: { type: bool }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", ""); err != nil {
		t.Errorf("Validate(distinct identifiers) = %v, want nil", err)
	}
}

func TestValidate_verbatimHelpString(t *testing.T) {
	// command.help / spec.help is a plain string (the verbatim page).
	spec := validSpecHeader + "command:\n  name: demo\n  help: |\n    my exact help page\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := validateOnce(path, "", ""); err != nil {
		t.Errorf("Validate(verbatim help string) = %v, want nil", err)
	}

	// help as an object is rejected (it must be a string now).
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","help":{"summary":"x"}}}`
	objPath := writeTemp(t, "obj.json", doc)
	if err := validateOnce(objPath, "", ""); err == nil {
		t.Fatal("expected error for help given as an object")
	}
}

func TestValidate_manMarkdownFields(t *testing.T) {
	// Spec: verbatim man/markdown strings on a command validate (mirror of help).
	spec := validSpecHeader + "command:\n  name: demo\n  man: |\n    DEMO(1)\n  markdown: |\n    # demo\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", spec), "", ""); err != nil {
		t.Errorf("Validate(spec with man/markdown) = %v, want nil", err)
	}

	// Conf: features.man / features.markdown validate.
	plainSpec := writeTemp(t, "spec2.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := validConfHeader + "generate:\n  features:\n    man: { enabled: true }\n    markdown: { enabled: true, dir: docs }\n"
	if err := validateOnce(plainSpec, writeTemp(t, "conf.yaml", conf), ""); err != nil {
		t.Errorf("Validate(conf with man/markdown features) = %v, want nil", err)
	}
}

// multiViolationDoc is a spec with two unknown properties → two schema violations,
// used to tell fast (one problem) from collect (all problems) apart.
const multiViolationDoc = `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","bogusA":1,"bogusB":2}}`

func TestValidate_failMode(t *testing.T) {
	path := writeTemp(t, "spec.json", multiViolationDoc)

	collect := validateOnce(path, "", "collect")
	fast := validateOnce(path, "", "fast")
	if collect == nil || fast == nil {
		t.Fatal("expected validation errors in both modes")
	}
	collectN := strings.Count(collect.Error(), "\n") + 1
	fastN := strings.Count(fast.Error(), "\n") + 1
	if collectN < 2 {
		t.Fatalf("fixture should yield multiple violations, got %d:\n%v", collectN, collect)
	}
	if fastN != 1 {
		t.Errorf("fast mode should report a single problem, got %d:\n%v", fastN, fast)
	}
}

func TestValidate_failModeFromConf(t *testing.T) {
	tmp := t.TempDir()
	writeTestFile(t, filepath.Join(tmp, "go.mod"), "module example.com/x\n\ngo 1.26\n")
	writeTestFile(t, filepath.Join(tmp, ".rotini.conf.yaml"),
		"$schema: https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/0.0.0/schema-conf.json\n"+
			"validate:\n  fail: fast\n")
	specPath := filepath.Join(tmp, "bad.json")
	writeTestFile(t, specPath, multiViolationDoc)
	t.Chdir(tmp)

	// Empty failMode resolves from the module-root conf (fast → single problem).
	err := validateOnce(specPath, "", "")
	if err == nil {
		t.Fatal("expected validation error")
	}
	if n := strings.Count(err.Error(), "\n") + 1; n != 1 {
		t.Errorf("conf validate.fail=fast should yield a single problem, got %d:\n%v", n, err)
	}
}

func TestValidate_importConsistency(t *testing.T) {
	// Same type `foo.Bar` declared with two different imports → a consistency error.
	bad := validSpecHeader +
		"command:\n" +
		"  name: mycli\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: a\n" +
		"        identifiers: [--a]\n" +
		"        schema: { type: foo.Bar, import: github.com/x/foo }\n" +
		"      - name: b\n" +
		"        identifiers: [--b]\n" +
		"        schema: { type: foo.Bar, import: github.com/y/foo }\n"
	err := validateOnce(writeTemp(t, "bad.yaml", bad), "", "collect")
	if err == nil {
		t.Fatal("expected an import-consistency error for one type with two imports")
	}
	if !strings.Contains(err.Error(), "conflicting imports") || !strings.Contains(err.Error(), "foo.Bar") {
		t.Errorf("error does not identify the conflict: %v", err)
	}

	// The same type with the SAME import everywhere is fine.
	ok := validSpecHeader +
		"command:\n" +
		"  name: mycli\n" +
		"  inputs:\n" +
		"    flags:\n" +
		"      - name: a\n" +
		"        identifiers: [--a]\n" +
		"        schema: { type: foo.Bar, import: github.com/x/foo }\n" +
		"      - name: b\n" +
		"        identifiers: [--b]\n" +
		"        schema: { type: foo.Bar, import: github.com/x/foo }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", "collect"); err != nil {
		t.Errorf("consistent imports should validate, got: %v", err)
	}
}

func TestValidate_badSchemaURL(t *testing.T) {
	path := writeTemp(t, "spec.yaml", "$schema: https://example.com/wrong\ncommand:\n  name: demo\n")
	if err := validateOnce(path, "", ""); err == nil {
		t.Fatal("expected error for $schema not matching the version pattern")
	}
}

func TestValidate_missingFile(t *testing.T) {
	if err := validateOnce(filepath.Join(t.TempDir(), "nope.yaml"), "", ""); err == nil {
		t.Fatal("expected error for missing spec file")
	}
}

func TestValidate_missingConfFile(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	err := validateOnce(spec, filepath.Join(t.TempDir(), "nope.yaml"), "")
	if err == nil {
		t.Fatal("expected error for specified-but-missing conf file")
	}
}

func TestValidate_emptySpecPath(t *testing.T) {
	err := validateOnce("", "", "")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("Validate(\"\", \"\") = %v, want a 'required' error", err)
	}
}

func TestValidate_invalidConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader+"bogus: true\n")
	err := validateOnce(spec, conf, "")
	if err == nil {
		t.Fatal("expected error for invalid conf")
	}
	if !strings.Contains(err.Error(), "conf") {
		t.Errorf("error does not identify the conf file: %v", err)
	}
}
