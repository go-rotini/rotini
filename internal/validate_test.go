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
	path := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	if err := Validate(path, ""); err != nil {
		t.Errorf("Validate(valid spec) = %v, want nil", err)
	}
}

func TestValidate_validSpecAndConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader)
	if err := Validate(spec, conf); err != nil {
		t.Errorf("Validate(valid spec+conf) = %v, want nil", err)
	}
}

func TestValidate_missingRequiredField(t *testing.T) {
	// Required "name" is absent.
	path := writeTemp(t, "spec.yaml", validSpecHeader)
	err := Validate(path, "")
	if err == nil {
		t.Fatal("expected error for spec missing required name")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error does not mention the missing field: %v", err)
	}
}

func TestValidate_unknownField(t *testing.T) {
	// additionalProperties:false must reject unknown fields; this only
	// works because validation runs on the raw instance, not a decoded
	// struct (which would silently drop "bogus").
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","name":"demo","bogus":true}`
	path := writeTemp(t, "spec.json", doc)
	if err := Validate(path, ""); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestValidate_helpKeys(t *testing.T) {
	spec := validSpecHeader +
		"name: demo\n" +
		"help:\n" +
		"  summary: a demo\n" +
		"  description: A demo CLI.\n" +
		"  usage: demo [flags]\n" +
		"  headings:\n" +
		"    commands: Subcommands\n" +
		"  examples: [demo run]\n" +
		"commands:\n" +
		"  - name: run\n" +
		"    hidden: true\n" +
		"    deprecated: use start\n" +
		"    help:\n" +
		"      summary: run it\n" +
		"    inputs:\n" +
		"      flags:\n" +
		"        - name: force\n" +
		"          summary: force it\n" +
		"          hidden: true\n" +
		"          deprecated: no longer needed\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := Validate(path, ""); err != nil {
		t.Errorf("Validate(spec with help keys) = %v, want nil", err)
	}
}

func TestValidate_helpTextRequiresManual(t *testing.T) {
	// help.text without mode: manual must fail the if/then conditional.
	spec := validSpecHeader + "name: demo\nhelp:\n  text: just text\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := Validate(path, ""); err == nil {
		t.Fatal("expected error for help.text without mode: manual")
	}

	// With mode: manual it is valid.
	ok := validSpecHeader + "name: demo\nhelp:\n  mode: manual\n  text: just text\n"
	okPath := writeTemp(t, "ok.yaml", ok)
	if err := Validate(okPath, ""); err != nil {
		t.Errorf("Validate(help.text + manual) = %v, want nil", err)
	}
}

func TestValidate_invalidHelpMode(t *testing.T) {
	spec := validSpecHeader + "name: demo\nhelp:\n  mode: bogus\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := Validate(path, ""); err == nil {
		t.Fatal("expected error for invalid help.mode")
	}
}

func TestValidate_unknownHelpKey(t *testing.T) {
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","name":"demo","help":{"bogus":true}}`
	path := writeTemp(t, "spec.json", doc)
	if err := Validate(path, ""); err == nil {
		t.Fatal("expected error for unknown key under help")
	}
}

func TestValidate_badSchemaURL(t *testing.T) {
	path := writeTemp(t, "spec.yaml", "$schema: https://example.com/wrong\nname: demo\n")
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
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
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
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader+"bogus: true\n")
	err := Validate(spec, conf)
	if err == nil {
		t.Fatal("expected error for invalid conf")
	}
	if !strings.Contains(err.Error(), "conf") {
		t.Errorf("error does not identify the conf file: %v", err)
	}
}
