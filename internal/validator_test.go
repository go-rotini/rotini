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
	if err := validateOnce(path, "", "", ""); err != nil {
		t.Errorf("Validate(valid spec) = %v, want nil", err)
	}
}

func TestValidate_validSpecAndConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader)
	if err := validateOnce(spec, conf, "", ""); err != nil {
		t.Errorf("Validate(valid spec+conf) = %v, want nil", err)
	}
}

// TestCheckSchemaVersion covers the Item-3 guard logic directly (all branches),
// since the $schema field is pattern-constrained to the rotini refs/tags form so
// the non-rotini/absent cases can't be reached through a schema-valid document.
func TestCheckSchemaVersion(t *testing.T) {
	const rotiniSpecURL = "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json"
	cases := []struct {
		name      string
		docSchema string
		ref       string
		wantErr   bool
	}{
		{"matching version passes", rotiniSpecURL, "1.2.3", false},
		{"mismatched version errors", rotiniSpecURL, "2.0.0", true},
		{"empty ref skips (dev/pseudo build)", rotiniSpecURL, "", false},
		{"non-rotini $schema is left alone", "https://example.com/schema.json", "1.2.3", false},
		{"absent $schema is left alone", "", "1.2.3", false},
		{"branch ref is not the recognized form", "https://raw.githubusercontent.com/go-rotini/rotini/refs/heads/main/schema-spec.json", "1.2.3", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSchemaVersion("spec", tc.docSchema, tc.ref)
			if tc.wantErr && err == nil {
				t.Fatalf("checkSchemaVersion(%q, ref=%q) = nil, want an error", tc.docSchema, tc.ref)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("checkSchemaVersion(%q, ref=%q) = %v, want nil", tc.docSchema, tc.ref, err)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "$schema") {
				t.Errorf("error %v does not mention $schema", err)
			}
		})
	}
}

// TestValidate_schemaVersionGuard confirms the guard is wired through the real
// validateOnce path: a non-empty ref enforces the spec/conf $schema version, an
// empty ref (dev/pseudo build) skips it.
func TestValidate_schemaVersionGuard(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n") // tag 1.2.3

	if err := validateOnce(spec, "", "", "1.2.3"); err != nil {
		t.Errorf("validateOnce(matching ref 1.2.3) = %v, want nil", err)
	}
	if err := validateOnce(spec, "", "", ""); err != nil {
		t.Errorf("validateOnce(empty ref) = %v, want nil (guard skipped)", err)
	}
	err := validateOnce(spec, "", "", "2.0.0")
	if err == nil {
		t.Fatal("validateOnce(mismatched ref 2.0.0) = nil, want a $schema error")
	}
	if !strings.Contains(err.Error(), "1.2.3") || !strings.Contains(err.Error(), "2.0.0") {
		t.Errorf("error %v should name both the doc version (1.2.3) and the binary version (2.0.0)", err)
	}

	// The conf's $schema is guarded too.
	conf := writeTemp(t, "conf.yaml", validConfHeader) // tag 1.2.3
	if cerr := validateOnce(spec, conf, "", "9.9.9"); cerr == nil || !strings.Contains(cerr.Error(), "conf") {
		t.Errorf("validateOnce(conf mismatch) = %v, want an error flagging the conf $schema", cerr)
	}
}

// TestValidate_singlePassRouting exercises the public Validate orchestrator (watch=false),
// which mirrors Generate: a valid pass hands a "[HH:MM:SS] <took>" summary to onValidate and
// returns nil; a failing pass returns the error and does NOT route it through the callback.
func TestValidate_singlePassRouting(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	var result string
	var cbErr error
	called := 0
	if err := Validate(spec, "", false, "", "", func(r string, e error) { called++; result, cbErr = r, e }); err != nil {
		t.Fatalf("Validate(valid) = %v, want nil", err)
	}
	if called != 1 || cbErr != nil || result == "" {
		t.Errorf("valid pass: called=%d result=%q err=%v, want one call with a summary and nil err", called, result, cbErr)
	}

	called = 0
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if err := Validate(missing, "", false, "", "", func(string, error) { called++ }); err == nil {
		t.Error("Validate(missing spec) = nil, want an error")
	}
	if called != 0 {
		t.Errorf("callback called %d times on a non-watch failure, want 0 (the error is returned)", called)
	}
}

func TestValidate_missingRequiredField(t *testing.T) {
	// The document IS the root command, so required "name" is absent at the
	// document level (W3 reshape — there is no "command" wrapper).
	path := writeTemp(t, "spec.yaml", validSpecHeader)
	err := validateOnce(path, "", "", "")
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
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","bogus":true}}`
	path := writeTemp(t, "spec.json", doc)
	if err := validateOnce(path, "", "", ""); err == nil {
		t.Fatal("expected error for unknown field")
	}
}

func TestValidate_helpKeys(t *testing.T) {
	// Flattened help fields on the root and on a command, plus input summaries
	// and hidden/deprecated, all validate.
	spec := validSpecHeader +
		"name: demo\n" +
		"summary: a demo\n" +
		"description: A demo CLI.\n" +
		"usage: demo [flags]\n" +
		"headings:\n" +
		"  commands: Subcommands\n" +
		"examples: [demo run]\n" +
		"commands:\n" +
		"  - name: run\n" +
		"    summary: run it\n" +
		"    hidden: true\n" +
		"    deprecated: use start\n" +
		"    flags:\n" +
		"      - name: force\n" +
		"        summary: force it\n" +
		"        hidden: true\n" +
		"        deprecated: no longer needed\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := validateOnce(path, "", "", ""); err != nil {
		t.Errorf("Validate(spec with help keys) = %v, want nil", err)
	}
}

func TestValidate_localTimeoutRejected(t *testing.T) {
	// timeout on a (local) sub-command is a remote-only concept — validation rejects
	// it (and the walk reaches nested commands).
	spec := validSpecHeader +
		"name: demo\n" +
		"commands:\n" +
		"  - name: run\n" +
		"    timeout: 5s\n"
	path := writeTemp(t, "spec.yaml", spec)
	err := validateOnce(path, "", "", "")
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "remote") {
		t.Errorf("Validate(local timeout) = %v, want a timeout/remote rejection", err)
	}
}

// TestValidate_rootAliases pins the F3 lint (spec-fidelity plan): aliases and
// deprecated_identifiers are sub-command routing surface — the root has no
// routing token, so declaring them there was an accepted lie. Sub-command
// aliasing is unaffected.
// TestValidate_constraintApplicability pins the R1 lint: a constraint
// declared on a type it can never check is rejected — it was previously
// accepted and silently ignored at parse time.
func TestValidate_constraintApplicability(t *testing.T) {
	make_ := func(flag string) string {
		return validSpecHeader + "name: app\nflags:\n" + flag
	}
	cases := []struct{ name, flag, want string }{
		{"bounds on string", "  - name: x\n    schema: { type: string, minimum: 1 }\n", "numeric types only"},
		{"bounds on duration", "  - name: ttl\n    schema: { type: duration, maximum: 60 }\n", "duration bounds are not supported"},
		{"bounds on imported type", "  - name: id\n    schema: { type: uuid.UUID, import: github.com/google/uuid, minimum: 1 }\n", "numeric types only"},
		{"pattern on int", "  - name: n\n    schema: { type: int, pattern: \"^x\" }\n", "string types only"},
		{"length on bool", "  - name: b\n    schema: { type: bool, minLength: 1 }\n", "string types only"},
		{"items on scalar", "  - name: s\n    schema: { type: string, minItems: 2 }\n", "repeatable (array/map) types only"},
		{"exclusive bound on string", "  - name: s\n    schema: { type: string, exclusiveMinimum: 0 }\n", "numeric types only"},
		{"multipleOf on bool", "  - name: b\n    schema: { type: bool, multipleOf: 2 }\n", "numeric types only"},
		{"exclusive bound on duration", "  - name: ttl\n    schema: { type: duration, exclusiveMaximum: 60 }\n", "duration bounds are not supported"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOnce(writeTemp(t, "spec.yaml", make_(c.flag)), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}

	// Applicable combinations pass: numeric bounds on uint, element bounds on
	// []int, element pattern on []string, counts on arrays and maps.
	valid := validSpecHeader + "name: app\nflags:\n" +
		"  - name: workers\n    schema: { type: uint, minimum: 1, maximum: 64 }\n" +
		"  - name: rate\n    schema: { type: float64, exclusiveMinimum: 0, exclusiveMaximum: 1 }\n" +
		"  - name: step\n    schema: { type: int, multipleOf: 5 }\n" +
		"  - name: port\n    schema: { type: array, items: { type: int }, minimum: 1, maxItems: 3 }\n" +
		"  - name: tag\n    schema: { type: \"[]string\", pattern: \"^[a-z]+$\", minItems: 1 }\n" +
		"  - name: label\n    schema: { type: map, maxItems: 5 }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(applicable constraints) = %v, want nil", err)
	}
}

// TestValidate_passthrough pins `passthrough: true`'s contract: no flags, no
// descent surface, and a mandatory variadic []string receiver.
func TestValidate_passthrough(t *testing.T) {
	wrap := func(body string) string {
		return validSpecHeader + "name: app\ncommands:\n  - name: exec\n    passthrough: true\n" + body
	}
	recv := "    arguments:\n      - name: cmdline\n        schema: { type: \"[]string\" }\n"
	cases := []struct{ name, body, want string }{
		{"flags rejected", recv + "    inputs2:\n", "declares flags"},
		{"sub-commands rejected", recv + "    commands:\n      - name: sub\n", "never descends"},
		{"missing receiver", "", "variadic []string"},
		{"non-string receiver",
			"    arguments:\n      - name: ns\n        schema: { type: array, items: { type: int } }\n",
			"variadic []string"},
	}
	// fix the "flags rejected" body: a passthrough command with both a flag and the receiver
	cases[0].body = "    flags:\n      - name: x\n        schema: { type: string }\n    arguments:\n      - name: cmdline\n        schema: { type: \"[]string\" }\n"
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOnce(writeTemp(t, "spec.yaml", wrap(c.body)), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}

	// The honest shape passes: a leading fixed argument plus the variadic receiver.
	valid := wrap("    arguments:\n      - name: program\n        schema: { type: string, required: true }\n      - name: cmdline\n        schema: { type: \"[]string\" }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(passthrough with receiver) = %v, want nil", err)
	}
}

// TestValidate_countFlags pins `type: count`'s contract: flag-channel only,
// and every value-shaped key is rejected — the tally is computed, never parsed.
func TestValidate_countFlags(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"count on env input",
			"env:\n  - name: depth\n    schema: { type: count }\n",
			"applies to flags only"},
		{"count on argument",
			"arguments:\n  - name: depth\n    schema: { type: count }\n",
			"applies to flags only"},
		{"default on count",
			"flags:\n  - name: v\n    schema: { type: count, default: 2 }\n",
			"do(es) not apply"},
		{"enum on count",
			"flags:\n  - name: v\n    schema: { type: count, enum: [one, two] }\n",
			"do(es) not apply"},
		{"bounds on count",
			"flags:\n  - name: v\n    schema: { type: count, maximum: 3 }\n",
			"do(es) not apply"},
		{"from on count",
			"flags:\n  - name: v\n    schema: { type: count, from: [stdin] }\n",
			"do(es) not apply"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := validSpecHeader + "name: app\n" + c.body
			err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}

	// The plain shape passes: identifiers, summary, hidden, deprecated.
	valid := validSpecHeader + "name: app\nflags:\n" +
		"  - name: verbose\n    identifiers: [--verbose, -v]\n    summary: Crank it up.\n    schema: { type: count }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(plain count flag) = %v, want nil", err)
	}
}

// TestValidate_patternCompiles pins the R-detour rule: a typo'd pattern would
// otherwise silently never enforce (the runtime tolerates a failed compile).
func TestValidate_patternCompiles(t *testing.T) {
	bad := validSpecHeader + "name: app\nflags:\n" +
		"  - name: title\n    schema: { type: string, pattern: \"([unclosed\" }\n"
	err := validateOnce(writeTemp(t, "spec.yaml", bad), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "does not compile") {
		t.Errorf("Validate(bad pattern) = %v, want the compile rejection", err)
	}
	good := validSpecHeader + "name: app\nflags:\n" +
		"  - name: title\n    schema: { type: string, pattern: \"^[a-z]+$\" }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", good), "", "", ""); err != nil {
		t.Errorf("Validate(good pattern) = %v, want nil", err)
	}
}

// TestValidate_zeroBounds pins the F4 fallback (spec-fidelity plan, F0-D4
// reversal): an explicit zero numeric bound would be silently ignored at
// every layer it travels (zero-sentinel float64s end to end), so validation
// rejects it loudly — everywhere in the document, including named schemas
// and stdin shapes, whose rendered validation schemas drop zeros too.
func TestValidate_zeroBounds(t *testing.T) {
	// The F4 fallback is REVERSED (R2-S7): bounds are presence-carrying
	// pointers end to end, so an explicit zero bound is ACCEPTED and enforced —
	// the old document-wide rejection is gone.
	cases := []struct{ name, body string }{
		{"flag minimum", "flags:\n  - name: port\n    schema: { type: int, minimum: 0, maximum: 10 }\n"},
		{"flag maximum", "flags:\n  - name: delta\n    schema: { type: int, maximum: 0 }\n"},
		{"named schema", "schemas:\n  Widget:\n    type: object\n    properties:\n      size: { type: integer, minimum: 0 }\n"},
		{"stdin shape", "stdin:\n  schema:\n    type: object\n    properties:\n      port: { type: integer, minimum: 0 }\n"},
		{"exclusive zero", "flags:\n  - name: rate\n    schema: { type: float64, exclusiveMinimum: 0 }\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := validSpecHeader + "name: app\n" + c.body
			if err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", ""); err != nil {
				t.Errorf("Validate = %v, want nil — a zero bound is a real, enforced bound now", err)
			}
		})
	}
}

// TestValidate_initializeLocation pins the F6 rule: an `initialize` block is
// honored only in the module-root conf — anywhere else it would be silently
// ignored, so validation rejects it; with no module above, the location is
// undecidable and the check is skipped.
func TestValidate_initializeLocation(t *testing.T) {
	confBody := validConfHeader + "initialize:\n  format: yaml\n"
	spec := func(t *testing.T, dir string) string {
		t.Helper()
		path := filepath.Join(dir, "spec.yaml")
		if err := os.WriteFile(path, []byte(validSpecHeader+"name: app\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	t.Run("module-root conf passes", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		conf := filepath.Join(dir, ".rotini.conf.yaml")
		if err := os.WriteFile(conf, []byte(confBody), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateOnce(spec(t, dir), conf, "", ""); err != nil {
			t.Errorf("Validate(root conf initialize) = %v, want nil", err)
		}
	})
	t.Run("nested conf is rejected", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/m\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(dir, "cmd", "app")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		conf := filepath.Join(nested, ".rotini.conf.yaml")
		if err := os.WriteFile(conf, []byte(confBody), 0o600); err != nil {
			t.Fatal(err)
		}
		err := validateOnce(spec(t, nested), conf, "", "")
		if err == nil || !strings.Contains(err.Error(), "module-root conf") {
			t.Errorf("Validate(nested conf initialize) = %v, want the move-to-root rejection", err)
		}
	})
	t.Run("no module above: undecidable, skipped", func(t *testing.T) {
		dir := t.TempDir() // no go.mod anywhere above a temp dir
		conf := filepath.Join(dir, ".rotini.conf.yaml")
		if err := os.WriteFile(conf, []byte(confBody), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateOnce(spec(t, dir), conf, "", ""); err != nil {
			t.Errorf("Validate(standalone conf initialize) = %v, want nil (skip)", err)
		}
	})
}

func TestValidate_rootAliases(t *testing.T) {
	rootAliases := validSpecHeader +
		"name: app\naliases: [ap]\n"
	err := validateOnce(writeTemp(t, "spec.yaml", rootAliases), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "root command cannot declare aliases") {
		t.Errorf("Validate(root aliases) = %v, want the targeted rejection", err)
	}

	rootDeprecated := validSpecHeader +
		"name: app\ndeprecated_identifiers: [old]\n"
	err = validateOnce(writeTemp(t, "spec.yaml", rootDeprecated), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "deprecated_identifiers") {
		t.Errorf("Validate(root deprecated_identifiers) = %v, want the targeted rejection", err)
	}

	subAliases := validSpecHeader +
		"name: app\ncommands:\n  - name: build\n    aliases: [b, bld]\n    deprecated_identifiers: [bld]\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", subAliases), "", "", ""); err != nil {
		t.Errorf("Validate(sub-command aliases) = %v, want nil — only the root is restricted", err)
	}
}

// TestValidate_remoteDescriptionRejected pins the F2 deletion (spec-fidelity
// plan): `description` on a remote command was schema-accepted but consumed by
// nothing — a remote's long-form docs belong to the remote binary itself. The
// key is now rejected, never silently ignored.
func TestValidate_remoteDescriptionRejected(t *testing.T) {
	spec := validSpecHeader +
		"name: demo\n" +
		"remote_commands:\n" +
		"  - name: plugin\n" +
		"    summary: a plugin\n" +
		"    description: belongs to the plugin, not here\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("Validate(remote description) = %v, want a rejection naming the field", err)
	}
}

func TestValidate_remoteTimeoutAccepted(t *testing.T) {
	// timeout on a remote_commands entry (host-side) is valid.
	spec := validSpecHeader +
		"name: demo\n" +
		"remote_commands:\n" +
		"  - name: plugin\n" +
		"    timeout: 10s\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := validateOnce(path, "", "", ""); err != nil {
		t.Errorf("Validate(remote_commands timeout) = %v, want nil", err)
	}
}

func TestValidate_dottedKeys(t *testing.T) {
	// dotted_keys on a map[string]any flag is the valid shape.
	valid := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: set\n" +
		"    schema: { type: map, dotted_keys: true }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(dotted_keys on a map flag) = %v, want nil", err)
	}

	// A typed-value map has nowhere to hang a subtree.
	typed := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: set\n" +
		"    schema: { type: \"map[string]string\", dotted_keys: true }\n"
	err := validateOnce(writeTemp(t, "spec.yaml", typed), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "dotted_keys") || !strings.Contains(err.Error(), "map[string]string") {
		t.Errorf("Validate(dotted_keys on map[string]string) = %v, want a type rejection", err)
	}

	// dotted assignment is command-line grammar: flags only.
	onEnv := validSpecHeader +
		"name: app\n" +
		"env:\n" +
		"  - name: overrides\n" +
		"    schema: { type: map, dotted_keys: true }\n"
	err = validateOnce(writeTemp(t, "spec.yaml", onEnv), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "flags only") {
		t.Errorf("Validate(dotted_keys on env input) = %v, want a flags-only rejection", err)
	}
}

func TestValidate_configurationFiles(t *testing.T) {
	make_ := func(entry string) string {
		// entry is a 2-space-indented config_files list, sitting directly under the
		// document-level config_files key.
		return validSpecHeader + "name: app\nconfig_files:\n" + entry
	}
	// Valid shapes: a fixed path, a walk-up discover, an xdg discover.
	valid := make_("" +
		"  - name: fixed\n    path: ~/.app.yaml\n" +
		"  - name: project\n    discover: { strategy: walk-up, file: .app.toml }\n" +
		"  - name: user\n    discover: { strategy: xdg, app: acme, file: config.yaml }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(valid config_files) = %v, want nil", err)
	}

	cases := []struct{ name, entry, want string }{
		{"no location", "  - name: lost\n", "set 'path' or 'discover'"},
		{"both locations", "  - name: both\n    path: x.yaml\n    discover: { strategy: xdg, app: a, file: f }\n", "exactly one"},
		{"xdg needs app", "  - name: u\n    discover: { strategy: xdg, file: f }\n", "needs 'app'"},
		{"walk-up rejects app", "  - name: p\n    discover: { strategy: walk-up, file: f, app: a }\n", "does not use 'app'"},
		{"unknown strategy", "  - name: s\n    discover: { strategy: registry, file: f }\n", "not in enum"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOnce(writeTemp(t, "spec.yaml", make_(c.entry)), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestValidate_configInputFile(t *testing.T) {
	make_ := func(inputs string) string {
		return validSpecHeader +
			"name: app\n" + inputs +
			"config_files:\n  - name: app\n    path: ~/.app.yaml\n"
	}
	valid := make_("config:\n  - name: endpoint\n    schema: { type: string, file: app, key: api.endpoint }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(pinned config input) = %v, want nil", err)
	}

	unknown := make_("config:\n  - name: endpoint\n    schema: { type: string, file: nope }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", unknown), "", "", ""); err == nil || !strings.Contains(err.Error(), "not a config_files entry in scope") {
		t.Errorf("Validate(unknown pin) = %v, want a rejection", err)
	}

	wrongChannel := make_("env:\n  - name: x\n    schema: { type: string, file: app }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", wrongChannel), "", "", ""); err == nil || !strings.Contains(err.Error(), "config inputs only") {
		t.Errorf("Validate(file on env) = %v, want a config-only rejection", err)
	}

	dupNames := validSpecHeader + "name: app\n" +
		"config_files:\n  - name: app\n    path: a.yaml\n  - name: app\n    path: b.yaml\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", dupNames), "", "", ""); err == nil || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("Validate(duplicate file names) = %v, want a uniqueness rejection", err)
	}
}

// TestValidate_configFileDuplicateLocationWarns pins the severity split: two
// config_files entries (distinct names) pointing at the SAME path in one command
// is a WARNING, not an error — validation passes, and the finding lands on the
// session's warnings (the OnWarning funnel's feed), not the returned error.
func TestValidate_configFileDuplicateLocationWarns(t *testing.T) {
	spec := validSpecHeader +
		"name: app\nconfig_files:\n" +
		"  - name: a\n    path: ~/.app.yaml\n" +
		"  - name: b\n    path: ~/.app.yaml\n"
	s := newSession(writeTemp(t, "spec.yaml", spec), "", "")
	if err := s.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := s.validate(); err != nil {
		t.Errorf("validate = %v, want nil (a duplicate location is a warning, not an error)", err)
	}
	if len(s.warnings) != 1 {
		t.Fatalf("warnings = %d, want exactly 1", len(s.warnings))
	}
	if !strings.Contains(s.warnings[0].Error(), "same file") {
		t.Errorf("warning = %q, want it to mention the same-file shadow", s.warnings[0])
	}
}

// TestValidate_configFilesChainScope pins the chain-aware config_files rules
// (D-W3.1): names cascade, so a child re-declaring an ancestor's name is an
// ERROR, while a sibling on a different branch may freely reuse it; a file: pin
// resolves up the chain (ancestor OK, sibling not); the same physical file across
// levels is a WARNING.
func TestValidate_configFilesChainScope(t *testing.T) {
	// Two sibling branches each declaring a config_files entry named "cfg" — a
	// reused name on independent chains is fine.
	siblingReuse := validSpecHeader + "name: app\ncommands:\n" +
		"  - name: a\n    config_files:\n      - name: cfg\n        path: a.yaml\n" +
		"  - name: b\n    config_files:\n      - name: cfg\n        path: b.yaml\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", siblingReuse), "", "", ""); err != nil {
		t.Errorf("Validate(sibling name reuse) = %v, want nil (independent chains)", err)
	}

	// A child re-declaring the root's "app" entry shadows it along the chain.
	shadow := validSpecHeader + "name: app\n" +
		"config_files:\n  - name: app\n    path: ~/.app.yaml\ncommands:\n" +
		"  - name: deploy\n    config_files:\n      - name: app\n        path: ./deploy.yaml\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", shadow), "", "", ""); err == nil || !strings.Contains(err.Error(), "shadows the entry declared on ancestor") {
		t.Errorf("Validate(child shadows ancestor name) = %v, want a shadow rejection", err)
	}

	// A child config input may pin an ANCESTOR's entry (it's in scope via cascade).
	pinAncestor := validSpecHeader + "name: app\n" +
		"config_files:\n  - name: app\n    path: ~/.app.yaml\ncommands:\n" +
		"  - name: deploy\n    config:\n      - name: x\n        schema: { type: string, file: app, key: a.b }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", pinAncestor), "", "", ""); err != nil {
		t.Errorf("Validate(pin ancestor entry) = %v, want nil (cascade in scope)", err)
	}

	// But a sibling's entry is NOT in scope — branch b cannot pin branch a's "acfg".
	pinSibling := validSpecHeader + "name: app\ncommands:\n" +
		"  - name: a\n    config_files:\n      - name: acfg\n        path: a.yaml\n" +
		"  - name: b\n    config:\n      - name: x\n        schema: { type: string, file: acfg, key: a.b }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", pinSibling), "", "", ""); err == nil || !strings.Contains(err.Error(), "not a config_files entry in scope") {
		t.Errorf("Validate(pin sibling entry) = %v, want an out-of-scope rejection", err)
	}

	// Same physical file declared at two levels of one chain is a WARNING.
	crossLevelDup := validSpecHeader + "name: app\n" +
		"config_files:\n  - name: app\n    path: ~/.shared.yaml\ncommands:\n" +
		"  - name: deploy\n    config_files:\n      - name: deploy\n        path: ~/.shared.yaml\n"
	s := newSession(writeTemp(t, "spec.yaml", crossLevelDup), "", "")
	if err := s.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := s.validate(); err != nil {
		t.Errorf("validate(cross-level same file) = %v, want nil (it is a warning)", err)
	}
	if len(s.warnings) != 1 || !strings.Contains(s.warnings[0].Error(), "same file") {
		t.Errorf("warnings = %v, want exactly one same-file shadow warning", s.warnings)
	}
}

func TestValidate_envNesting(t *testing.T) {
	make_ := func(inputs string) string {
		return validSpecHeader + "name: app\n" + inputs
	}
	valid := make_("env:\n  - name: http\n    schema: { type: map, nesting: \"__\", variable: ACME_HTTP }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(nesting on map env input) = %v, want nil", err)
	}

	cases := []struct{ name, inputs, want string }{
		{"env only", "flags:\n  - name: x\n    schema: { type: map, nesting: \"__\" }\n", "env inputs only"},
		{"map typed", "env:\n  - name: x\n    schema: { type: string, nesting: \"__\" }\n", "map"},
		{"no default", "env:\n  - name: x\n    schema: { type: map, nesting: \"__\", default: y }\n", "no single default"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOnce(writeTemp(t, "spec.yaml", make_(c.inputs)), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestValidate_configSource(t *testing.T) {
	make_ := func(inputs string) string {
		return validSpecHeader +
			"name: app\n" + inputs +
			"config_files:\n  - name: app\n    path: ~/.app.yaml\n"
	}
	valid := make_("" +
		"flags:\n  - name: config\n    schema: { type: string, config_source: app }\n" +
		"env:\n  - name: config_path\n    schema: { type: string, config_source: app }\n")
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(config_source on flag+env) = %v, want nil", err)
	}

	cases := []struct{ name, inputs, want string }{
		{"unknown entry",
			"flags:\n  - name: c\n    schema: { type: string, config_source: nope }\n",
			"not a config_files entry in scope"},
		{"flag and env only",
			"config:\n  - name: c\n    schema: { type: string, config_source: app }\n",
			"flag and env inputs only"},
		{"string typed",
			"flags:\n  - name: c\n    schema: { type: int, config_source: app }\n",
			"a file path is a string"},
		{"one flag per entry",
			"flags:\n  - name: a\n    schema: { type: string, config_source: app }\n" +
				"  - name: b\n    schema: { type: string, config_source: app }\n",
			"already claimed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateOnce(writeTemp(t, "spec.yaml", make_(c.inputs)), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}

	// config_source claims cascade: a child flag claiming the same entry as the
	// root's flag collides along the chain (one flag per entry per chain).
	chainConflict := validSpecHeader + "name: app\n" +
		"config_files:\n  - name: app\n    path: ~/.app.yaml\n" +
		"flags:\n  - name: root_cfg\n    schema: { type: string, config_source: app }\ncommands:\n" +
		"  - name: deploy\n    flags:\n      - name: deploy_cfg\n        schema: { type: string, config_source: app }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", chainConflict), "", "", ""); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("Validate(cross-level claim conflict) = %v, want an already-claimed rejection", err)
	}

	// A SIBLING claiming the root entry is fine — its own chain has one claim.
	siblingClaim := validSpecHeader + "name: app\n" +
		"config_files:\n  - name: app\n    path: ~/.app.yaml\ncommands:\n" +
		"  - name: a\n    flags:\n      - name: a_cfg\n        schema: { type: string, config_source: app }\n" +
		"  - name: b\n    flags:\n      - name: b_cfg\n        schema: { type: string, config_source: app }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", siblingClaim), "", "", ""); err != nil {
		t.Errorf("Validate(sibling claims of ancestor entry) = %v, want nil (independent chains)", err)
	}
}

func TestValidate_from(t *testing.T) {
	// from: on a string flag is the valid shape (file + stdin).
	valid := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: token\n" +
		"    schema: { type: string, from: [value, file] }\n" +
		"  - name: payload\n" +
		"    schema: { type: string, from: [stdin] }\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", valid), "", "", ""); err != nil {
		t.Errorf("Validate(from on flags) = %v, want nil", err)
	}

	cases := []struct {
		name, body, want string
	}{
		{"flags only",
			"env:\n  - name: token\n    schema: { type: string, from: [file] }\n",
			"flags only"},
		{"never bool",
			"flags:\n  - name: loud\n    schema: { type: bool, from: [file] }\n",
			"bool"},
		{"stdin channel conflict",
			"flags:\n  - name: f\n    schema: { type: string, from: [stdin] }\n" +
				"stdin:\n  schema: { type: object }\n",
			"one consumer"},
		{"two stdin flags conflict",
			"flags:\n  - name: a\n    schema: { type: string, from: [stdin] }\n" +
				"  - name: b\n    schema: { type: string, from: [stdin] }\n",
			"one consumer"},
		{"unknown mode rejected by the schema",
			"flags:\n  - name: t\n    schema: { type: string, from: [filesystem] }\n",
			"not in enum"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := validSpecHeader + "name: app\n" + c.body
			err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestValidate_flagGroupUnknownFlag(t *testing.T) {
	// A flag_groups entry referencing a flag the command doesn't declare is rejected.
	spec := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: json\n" +
		"    schema: { type: bool }\n" +
		"flag_groups:\n" +
		"  - kind: mutually_exclusive\n" +
		"    flags: [json, nope]\n"
	path := writeTemp(t, "spec.yaml", spec)
	err := validateOnce(path, "", "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "nope") {
		t.Errorf("Validate(flag group with unknown flag) = %v, want an unknown-flag error", err)
	}
}

func TestValidate_danglingSchemaRef(t *testing.T) {
	// A $ref to a schema the document doesn't declare is rejected (it would otherwise be
	// an "undefined type" compile error in the generated code), with a suggestion.
	spec := validSpecHeader +
		"name: app\n" +
		"config:\n" +
		"  - name: server\n" +
		"    schema: { $ref: '#/schemas/Endpiont' }\n" +
		"schemas:\n" +
		"  Endpoint:\n" +
		"    type: object\n" +
		"    properties:\n" +
		"      host: { type: string }\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "undeclared schema") || !strings.Contains(err.Error(), `did you mean "Endpoint"`) {
		t.Errorf("Validate(dangling $ref) = %v, want an undeclared-schema error suggesting Endpoint", err)
	}

	// A valid $ref to a declared schema passes.
	ok := validSpecHeader +
		"name: app\n" +
		"config:\n" +
		"  - name: server\n" +
		"    schema: { $ref: '#/schemas/Endpoint' }\n" +
		"schemas:\n" +
		"  Endpoint:\n" +
		"    type: object\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", "", ""); err != nil {
		t.Errorf("Validate(valid $ref) = %v, want nil", err)
	}
}

func TestValidate_flagGroupSuggestion(t *testing.T) {
	// A flag_groups reference that's a near-typo of a real flag gets a "did you mean".
	spec := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: json\n" +
		"    schema: { type: bool }\n" +
		"flag_groups:\n" +
		"  - kind: mutually_exclusive\n" +
		"    flags: [json, jsno]\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
	if err == nil || !strings.Contains(err.Error(), `did you mean "json"`) {
		t.Errorf("Validate(flag group typo) = %v, want a did-you-mean suggestion", err)
	}
}

func TestValidate_flagDependencyUnknownFlag(t *testing.T) {
	// A flag_dependencies entry whose 'requires' names a flag the command doesn't
	// declare is rejected (the 'when' flag is checked the same way).
	spec := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: tls\n" +
		"    schema: { type: bool }\n" +
		"flag_dependencies:\n" +
		"  - when: tls\n" +
		"    requires: [cert]\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "unknown flag") || !strings.Contains(err.Error(), "cert") {
		t.Errorf("Validate(flag dependency requiring unknown flag) = %v, want an unknown-flag error", err)
	}
}

func TestValidate_duplicateFlagIdentifier(t *testing.T) {
	// Two flags claiming the same explicit identifier (-o) on one command is rejected.
	spec := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: output\n" +
		"    identifiers: [-o, --output]\n" +
		"    schema: { type: string }\n" +
		"  - name: organization\n" +
		"    identifiers: [-o, --org]\n" +
		"    schema: { type: string }\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
	if err == nil || !strings.Contains(err.Error(), `"-o"`) || !strings.Contains(err.Error(), "output") {
		t.Errorf("Validate(duplicate -o) = %v, want a duplicate-identifier error naming -o and output", err)
	}

	// Auto-derived collision across spellings: "dry_run" and "dry-run" both
	// derive --dry-run (the lint shares codegen's exact derivation).
	mixed := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: dry_run\n" +
		"    schema: { type: bool }\n" +
		"  - name: dry-run\n" +
		"    schema: { type: bool }\n"
	if err := validateOnce(writeTemp(t, "mixed.yaml", mixed), "", "", ""); err == nil || !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("Validate(dry_run vs dry-run) = %v, want a --dry-run collision", err)
	}

	// Auto-derived collision: two flags whose names both yield --out (no identifiers).
	spec2 := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: out\n" +
		"    schema: { type: string }\n" +
		"  - name: out\n" +
		"    schema: { type: bool }\n"
	if err := validateOnce(writeTemp(t, "spec2.yaml", spec2), "", "", ""); err == nil || !strings.Contains(err.Error(), "--out") {
		t.Errorf("Validate(derived --out collision) = %v, want a duplicate-identifier error", err)
	}

	// Distinct identifiers validate cleanly.
	ok := validSpecHeader +
		"name: app\n" +
		"flags:\n" +
		"  - name: output\n" +
		"    identifiers: [-o, --output]\n" +
		"    schema: { type: string }\n" +
		"  - name: verbose\n" +
		"    identifiers: [-v, --verbose]\n" +
		"    schema: { type: bool }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", "", ""); err != nil {
		t.Errorf("Validate(distinct identifiers) = %v, want nil", err)
	}
}

func TestValidate_verbatimHelpString(t *testing.T) {
	// command.help / spec.help is a plain string (the verbatim page).
	spec := validSpecHeader + "name: demo\nhelp: |\n  my exact help page\n"
	path := writeTemp(t, "spec.yaml", spec)
	if err := validateOnce(path, "", "", ""); err != nil {
		t.Errorf("Validate(verbatim help string) = %v, want nil", err)
	}

	// help as an object is rejected (it must be a string now).
	doc := `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","help":{"summary":"x"}}}`
	objPath := writeTemp(t, "obj.json", doc)
	if err := validateOnce(objPath, "", "", ""); err == nil {
		t.Fatal("expected error for help given as an object")
	}
}

func TestValidate_manFields(t *testing.T) {
	// Spec: a verbatim man string on a command validates (mirror of help).
	spec := validSpecHeader + "name: demo\nman: |\n  DEMO(1)\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", ""); err != nil {
		t.Errorf("Validate(spec with man) = %v, want nil", err)
	}

	// Conf: the features.man toggle validates.
	plainSpec := writeTemp(t, "spec2.yaml", validSpecHeader+"name: demo\n")
	conf := validConfHeader + "generate:\n  features:\n    man: { enabled: true }\n"
	if err := validateOnce(plainSpec, writeTemp(t, "conf.yaml", conf), "", ""); err != nil {
		t.Errorf("Validate(conf with man feature) = %v, want nil", err)
	}
}

// TestValidate_handlerFilenameCollision rejects two commands that resolve to the same
// generated stub file — here "test" and "test_", which both escape to "app_test_.go".
func TestValidate_handlerFilenameCollision(t *testing.T) {
	spec := validSpecHeader + "name: app\ncommands:\n  - name: test\n  - name: test_\n"
	err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
	if err == nil || !strings.Contains(err.Error(), "already used by command") {
		t.Errorf("Validate(colliding stub filenames) = %v, want a collision error", err)
	}
}

// TestValidate_handlerFilenameOverride covers the `filename` override rules: it must be
// a bare "*.go" name that Go does not read specially; a valid, unique one passes.
func TestValidate_handlerFilenameOverride(t *testing.T) {
	for _, tc := range []struct{ name, filename, wantErr string }{
		{"reserved", "app_test.go", "read specially"},     // Go-side: the pattern can't know _test/GOOS/GOARCH
		{"noGoExt", "handlers", "does not match pattern"}, // schema-rejected (R-detour: rule codified)
		{"hasDir", "sub/x.go", "does not match pattern"},  // schema-rejected
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpecHeader + "name: app\ncommands:\n  - name: build\n    filename: " + tc.filename + "\n"
			err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", "")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Validate(filename=%q) = %v, want error containing %q", tc.filename, err, tc.wantErr)
			}
		})
	}

	spec := validSpecHeader + "name: app\ncommands:\n  - name: build\n    filename: my_handlers.go\n"
	if err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "", ""); err != nil {
		t.Errorf("Validate(valid filename override) = %v, want nil", err)
	}
}

// multiViolationDoc is a spec with two unknown properties → two schema violations,
// used to tell fast (one problem) from collect (all problems) apart.
const multiViolationDoc = `{"$schema":"https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json","command":{"name":"demo","bogusA":1,"bogusB":2}}`

func TestValidate_failMode(t *testing.T) {
	path := writeTemp(t, "spec.json", multiViolationDoc)

	collect := validateOnce(path, "", "collect", "")
	fast := validateOnce(path, "", "fast", "")
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
	err := validateOnce(specPath, "", "", "")
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
		"name: mycli\n" +
		"flags:\n" +
		"  - name: a\n" +
		"    identifiers: [--a]\n" +
		"    schema: { type: foo.Bar, import: github.com/x/foo }\n" +
		"  - name: b\n" +
		"    identifiers: [--b]\n" +
		"    schema: { type: foo.Bar, import: github.com/y/foo }\n"
	err := validateOnce(writeTemp(t, "bad.yaml", bad), "", "collect", "")
	if err == nil {
		t.Fatal("expected an import-consistency error for one type with two imports")
	}
	if !strings.Contains(err.Error(), "conflicting imports") || !strings.Contains(err.Error(), "foo.Bar") {
		t.Errorf("error does not identify the conflict: %v", err)
	}

	// The same type with the SAME import everywhere is fine.
	ok := validSpecHeader +
		"name: mycli\n" +
		"flags:\n" +
		"  - name: a\n" +
		"    identifiers: [--a]\n" +
		"    schema: { type: foo.Bar, import: github.com/x/foo }\n" +
		"  - name: b\n" +
		"    identifiers: [--b]\n" +
		"    schema: { type: foo.Bar, import: github.com/x/foo }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", "collect", ""); err != nil {
		t.Errorf("consistent imports should validate, got: %v", err)
	}
}

func TestValidate_badSchemaURL(t *testing.T) {
	path := writeTemp(t, "spec.yaml", "$schema: https://example.com/wrong\nname: demo\n")
	if err := validateOnce(path, "", "", ""); err == nil {
		t.Fatal("expected error for $schema not matching the version pattern")
	}
}

func TestValidate_missingFile(t *testing.T) {
	if err := validateOnce(filepath.Join(t.TempDir(), "nope.yaml"), "", "", ""); err == nil {
		t.Fatal("expected error for missing spec file")
	}
}

func TestValidate_missingConfFile(t *testing.T) {
	// A specified-but-missing conf is tolerated: the conf is optional, so validation
	// falls back to defaults rather than erroring.
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	if err := validateOnce(spec, filepath.Join(t.TempDir(), "nope.yaml"), "", ""); err != nil {
		t.Errorf("Validate(missing conf) = %v, want nil (defaults used)", err)
	}
}

func TestValidate_emptySpecPath(t *testing.T) {
	err := validateOnce("", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Errorf("Validate(\"\", \"\") = %v, want a 'required' error", err)
	}
}

func TestValidate_invalidConf(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	conf := writeTemp(t, "conf.yaml", validConfHeader+"bogus: true\n")
	err := validateOnce(spec, conf, "", "")
	if err == nil {
		t.Fatal("expected error for invalid conf")
	}
	if !strings.Contains(err.Error(), "conf") {
		t.Errorf("error does not identify the conf file: %v", err)
	}
}

// TestValidate_confFilePattern: the schema rejects a generated-file name that is
// not a bare *.go (generate would otherwise emit Go source into it blindly).
func TestValidate_confFilePattern(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")
	for _, bad := range []string{"not-go.txt", "sub/x.go"} {
		conf := writeTemp(t, "conf.yaml", validConfHeader+"generate:\n  packages:\n    cmd:\n      file: "+bad+"\n")
		if err := validateOnce(spec, conf, "", ""); err == nil || !strings.Contains(err.Error(), "pattern") {
			t.Errorf("validate(file=%q) = %v, want a pattern violation", bad, err)
		}
	}
}

// TestValidate_entrypointLints: accepted-but-ignored entrypoint pieces are
// rejected rather than silently dropped — file/keep without the required
// package, and keep at all (the entrypoint is never pruned).
func TestValidate_entrypointLints(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")

	orphan := writeTemp(t, "conf.yaml", validConfHeader+"generate:\n  packages:\n    entrypoint:\n      file: main.go\n")
	if err := validateOnce(spec, orphan, "", ""); err == nil || !strings.Contains(err.Error(), "entrypoint.package is set") {
		t.Errorf("validate(entrypoint file without package) = %v, want an entrypoint lint", err)
	}

	keep := writeTemp(t, "conf2.yaml", validConfHeader+"generate:\n  packages:\n    entrypoint:\n      package: cmd/demo\n      keep: [main.go]\n")
	if err := validateOnce(spec, keep, "", ""); err == nil || !strings.Contains(err.Error(), "never pruned") {
		t.Errorf("validate(entrypoint keep) = %v, want a keep-has-no-effect lint", err)
	}

	ok := writeTemp(t, "conf3.yaml", validConfHeader+"generate:\n  packages:\n    entrypoint:\n      package: cmd/demo\n      file: main.go\n")
	if err := validateOnce(spec, ok, "", ""); err != nil {
		t.Errorf("validate(well-formed entrypoint) = %v, want nil", err)
	}
}

// TestValidate_featureDirLint: an enabled feature whose explicit dir cannot
// nest under the explicit cmdgen package fails at validate time (generate would
// reject it later — validate is the gate). Unset sides defer to the defaults.
func TestValidate_featureDirLint(t *testing.T) {
	spec := writeTemp(t, "spec.yaml", validSpecHeader+"name: demo\n")

	cases := []struct {
		name, dir string
		wantErr   bool
	}{
		{"outside the package", "docs/help", true},
		{"escapes via ..", "internal/cmd/demo/../../docs", true},
		{"under the package", "internal/cmd/demo/embed", false},
		{"the package itself", "internal/cmd/demo", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conf := writeTemp(t, "conf.yaml", validConfHeader+
				"generate:\n  packages:\n    cmdgen:\n      package: internal/cmd/demo\n"+
				"  features:\n    help:\n      enabled: true\n      embed: true\n      embed_dir: "+tc.dir+"\n")
			err := validateOnce(spec, conf, "", "")
			if tc.wantErr && (err == nil || !strings.Contains(err.Error(), "must resolve under the cmdgen package")) {
				t.Errorf("validate(dir=%q) = %v, want a feature-dir lint", tc.dir, err)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validate(dir=%q) = %v, want nil", tc.dir, err)
			}
		})
	}

	// Disabled features and unset cmdgen packages are not checked.
	lax := writeTemp(t, "lax.yaml", validConfHeader+
		"generate:\n  features:\n    help:\n      enabled: true\n      embed: true\n      embed_dir: docs/help\n")
	if err := validateOnce(spec, lax, "", ""); err != nil {
		t.Errorf("validate(dir without explicit cmdgen) = %v, want nil (generate backstops)", err)
	}
}

// TestValidate_newSpecLints exercises the validate-time rules added for spec
// gaps: root shape, sibling dispatch collisions (including remote shadowing),
// duplicate input names, variadic placement, deprecated_identifiers subsets,
// and remote timeouts.
func TestValidate_newSpecLints(t *testing.T) {
	cases := []struct {
		name, spec, want string
	}{
		{"root with $ref", "name: app\n$ref: ./other.yaml\n", "root command cannot use $ref"},
		{"sibling alias collision", "name: app\ncommands:\n  - name: build\n    aliases: [b]\n  - name: bundle\n    aliases: [b]\n", `"b" is claimed by both`},
		{"remote shadowed by command", "name: app\ncommands:\n  - name: plugin\nremote_commands:\n  - name: plugin\n", `"plugin" is claimed by both`},
		{"duplicate flag names", "name: app\nflags:\n  - name: out\n    identifiers: [-o]\n    schema: { type: string }\n  - name: out\n    identifiers: [-O]\n    schema: { type: bool }\n", `flag "out" is declared twice`},
		{"variadic not last", "name: app\narguments:\n  - name: files\n    schema: { type: array }\n  - name: dest\n    schema: { type: string }\n", `"files" is variadic but not last`},
		{"command deprecated_identifiers not an alias", "name: app\ncommands:\n  - name: compile\n    aliases: [build]\n    deprecated_identifiers: [biuld]\n", "not one of its aliases"},
		{"flag deprecated_identifiers not an identifier", "name: app\nflags:\n  - name: config\n    identifiers: [--config]\n    deprecated_identifiers: [--conf]\n    schema: { type: string }\n", "not one of its identifiers"},
		// The duration SHAPE is schema-codified (R-detour); the lint still owns
		// what the pattern can't say: a pattern-valid but non-positive duration.
		{"remote timeout malformed (schema)", "name: app\nremote_commands:\n  - name: plugin\n    timeout: ten-seconds\n", "does not match pattern"},
		{"remote timeout non-positive (lint)", "name: app\nremote_commands:\n  - name: plugin\n    timeout: 0s\n", "not a positive Go duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateOnce(writeTemp(t, "spec.yaml", validSpecHeader+tc.spec), "", "collect", "")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validate = %v, want a problem containing %q", err, tc.want)
			}
		})
	}

	// The derived flag identifier satisfies the subset rule (no explicit identifiers).
	ok := validSpecHeader + "name: app\nflags:\n  - name: dry_run\n    deprecated_identifiers: [--dry-run]\n    schema: { type: bool }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", "", ""); err != nil {
		t.Errorf("validate(derived identifier subset) = %v, want nil", err)
	}
}

// TestValidate_docLevelKeysOnSubcommand pins lintDocLevelKeys: the three
// document-level keys ($schema, env_prefix, schemas) are valid only on the root
// command (the document). The merged shape (W3) accepts them structurally on
// every command, so the lint rejects them on a sub-command; the root is exempt.
func TestValidate_docLevelKeysOnSubcommand(t *testing.T) {
	schemaURL := "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schema-spec.json"
	cases := []struct{ name, sub string }{
		{"$schema", "    $schema: " + schemaURL + "\n"},
		{"env_prefix", "    env_prefix: ACME\n"},
		{"schemas", "    schemas:\n      Foo: { type: string }\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpecHeader + "name: app\ncommands:\n  - name: sub\n" + tc.sub
			err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "collect", "")
			if err == nil || !strings.Contains(err.Error(), "document-level key valid only on the root command") {
				t.Errorf("validate(%s on sub) = %v, want a doc-level-key rejection", tc.name, err)
			}
		})
	}

	// The same keys on the ROOT are fine.
	root := validSpecHeader + "name: app\nenv_prefix: ACME\nschemas:\n  Foo: { type: string }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", root), "", "", ""); err != nil {
		t.Errorf("validate(doc-level keys on root) = %v, want nil", err)
	}
}

// TestValidate_refNodeRejectsHandlerCoupledKeys pins lintRefNodeKeys (W8 / D-W8.2):
// a $ref node composes a child whose handler is built against ITS own inputs/output,
// so handler-coupled keys on the $ref node are rejected (no silent ignore). Overlay
// keys and an additive `commands:` sibling are allowed.
func TestValidate_refNodeRejectsHandlerCoupledKeys(t *testing.T) {
	cases := []struct{ name, node, key string }{
		{"flags", "    flags:\n      - name: f\n        schema: { type: string }\n", "flags"},
		{"arguments", "    arguments:\n      - name: a\n        schema: { type: string }\n", "arguments"},
		{"env", "    env:\n      - name: e\n        schema: { type: string }\n", "env"},
		{"config_files", "    config_files:\n      - { name: c, path: ~/.x }\n", "config_files"},
		{"output", "    output: { type: string }\n", "output"},
		{"stdin", "    stdin:\n      schema: { type: object }\n", "stdin"},
		{"remote_discovery", "    remote_discovery:\n      path: /x\n", "remote_discovery"},
		{"passthrough", "    passthrough: true\n", "passthrough"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := validSpecHeader + "name: app\ncommands:\n  - $ref: ./child.yaml\n    name: kid\n" + tc.node
			err := validateOnce(writeTemp(t, "spec.yaml", spec), "", "collect", "")
			if err == nil || !strings.Contains(err.Error(), tc.key) || !strings.Contains(err.Error(), "$ref node") {
				t.Errorf("validate(%s on $ref node) = %v, want a $ref-node rejection naming %q", tc.name, err, tc.key)
			}
		})
	}

	// Overlay keys + an authored `commands:` sibling on a $ref node are ALLOWED (the
	// inline sibling's own inputs are fine — they're on `local`, not the $ref node).
	ok := validSpecHeader + "name: app\ncommands:\n  - $ref: ./child.yaml\n    name: kid\n" +
		"    summary: my kid\n    group: g\n    commands:\n      - name: local\n" +
		"        arguments:\n          - name: t\n            schema: { type: string }\n"
	if err := validateOnce(writeTemp(t, "ok.yaml", ok), "", "", ""); err != nil {
		t.Errorf("validate(overlay keys + commands on $ref node) = %v, want nil", err)
	}
}
