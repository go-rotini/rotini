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
	if err := Validate(path, ""); err != nil {
		t.Errorf("Validate(valid spec) = %v, want nil", err)
	}
}

func TestValidate_validSpecAndConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader)
	if err := Validate(spec, conf); err != nil {
		t.Errorf("Validate(valid spec+conf) = %v, want nil", err)
	}
}

func TestValidate_missingRequiredField(t *testing.T) {
	// Required "command" is absent at the document level.
	path := writeTemp(t, "spec.yaml", validSpecHeader)
	err := Validate(path, "")
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
	if err := Validate(path, ""); err == nil {
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
	if err := Validate(path, ""); err != nil {
		t.Errorf("Validate(spec with help keys) = %v, want nil", err)
	}
}

func TestValidate_verbatimHelpString(t *testing.T) {
	// command.help / spec.help is a plain string (the verbatim page).
	spec := validSpecHeader + "command:\n  name: demo\n  help: |\n    my exact help page\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := Validate(path, ""); err != nil {
		t.Errorf("Validate(verbatim help string) = %v, want nil", err)
	}

	// help as an object is rejected (it must be a string now).
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","help":{"summary":"x"}}}`
	objPath := writeTemp(t, "obj.json", doc)
	if err := Validate(objPath, ""); err == nil {
		t.Fatal("expected error for help given as an object")
	}
}

func TestValidate_manMarkdownFields(t *testing.T) {
	// Spec: verbatim man/markdown strings on a command validate (mirror of help).
	spec := validSpecHeader + "command:\n  name: demo\n  man: |\n    DEMO(1)\n  markdown: |\n    # demo\n"
	if err := Validate(writeTemp(t, "spec.yaml", spec), ""); err != nil {
		t.Errorf("Validate(spec with man/markdown) = %v, want nil", err)
	}

	// Conf: features.man / features.markdown validate.
	plainSpec := writeTemp(t, "spec2.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := validConfHeader + "generate:\n  rtg:\n    features:\n      man: { enabled: true }\n      markdown: { enabled: true, dir: docs }\n"
	if err := Validate(plainSpec, writeTemp(t, "conf.yaml", conf)); err != nil {
		t.Errorf("Validate(conf with man/markdown features) = %v, want nil", err)
	}
}

func TestValidate_badSchemaURL(t *testing.T) {
	path := writeTemp(t, "spec.yaml", "$schema: https://example.com/wrong\ncommand:\n  name: demo\n")
	if err := Validate(path, ""); err == nil {
		t.Fatal("expected error for $schema not matching the version pattern")
	}
}

func TestValidate_missingFile(t *testing.T) {
	if err := Validate(filepath.Join(t.TempDir(), "nope.yaml"), ""); err == nil {
		t.Fatal("expected error for missing spec file")
	}
}

func TestValidate_missingConfFile(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	err := Validate(spec, filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for specified-but-missing conf file")
	}
}

func TestValidate_emptySpecPath(t *testing.T) {
	err := Validate("", "")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("Validate(\"\", \"\") = %v, want a 'required' error", err)
	}
}

func TestValidate_invalidConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"command:\n  name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader+"bogus: true\n")
	err := Validate(spec, conf)
	if err == nil {
		t.Fatal("expected error for invalid conf")
	}
	if !strings.Contains(err.Error(), "conf") {
		t.Errorf("error does not identify the conf file: %v", err)
	}
}
