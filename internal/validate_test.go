package internal_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-rotini/rotini/internal"
)

// =============================================================================
// Helpers — minimal Spec builders for table-driven tests
// =============================================================================

// validSpec returns a Spec that passes both schema and semantic checks.
// Tests start from this baseline and mutate one field per case so each
// failure can be attributed to a single change.
func validSpec() *internal.Spec {
	return &internal.Spec{
		SchemaURL: "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schemas/spec.json",
		Name:      "greet",
	}
}

func mustErr(t *testing.T, err error, wantSubstr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", wantSubstr)
	}
	if !strings.Contains(err.Error(), wantSubstr) {
		t.Errorf("error %q does not contain %q", err.Error(), wantSubstr)
	}
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// =============================================================================
// Schema validation — top-level constraints
// =============================================================================

func TestValidate_minimalSpec(t *testing.T) {
	t.Parallel()
	mustNoErr(t, internal.Validate(validSpec()))
}

func TestValidate_nilSpec(t *testing.T) {
	t.Parallel()
	mustErr(t, internal.Validate(nil), "spec is nil")
}

func TestValidate_missingName(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Name = ""
	mustErr(t, internal.Validate(s), "name")
}

func TestValidate_invalidNamePattern(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Name = "bad name with spaces"
	mustErr(t, internal.Validate(s), "pattern")
}

func TestValidate_invalidSchemaURL(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.SchemaURL = "https://example.com/wrong"
	mustErr(t, internal.Validate(s), "pattern")
}

// =============================================================================
// Semantic — durations
// =============================================================================

func TestValidate_invalidRootTimeout(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Timeout = "not-a-duration"
	mustErr(t, internal.Validate(s), "duration")
}

func TestValidate_validTimeoutAccepted(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Timeout = "10s"
	mustNoErr(t, internal.Validate(s))
}

func TestValidate_emptyTimeoutAccepted(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Timeout = ""
	mustNoErr(t, internal.Validate(s))
}

// =============================================================================
// Semantic — duplicate command names (sibling routing namespace)
// =============================================================================

func TestValidate_duplicateCommandNames(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{Name: "list"},
		{Name: "list"},
	}
	err := internal.Validate(s)
	mustErr(t, err, "clashes")
}

func TestValidate_aliasClashesWithCommand(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{Name: "list"},
		{Name: "show", Aliases: []string{"list"}},
	}
	mustErr(t, internal.Validate(s), "clashes")
}

func TestValidate_duplicateAliases(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{Name: "first", Aliases: []string{"ls"}},
		{Name: "second", Aliases: []string{"ls"}},
	}
	mustErr(t, internal.Validate(s), "clashes")
}

// =============================================================================
// Semantic — flag duplicate names / identifiers
// =============================================================================

func TestValidate_duplicateFlagNames(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Inputs = &internal.Inputs{
		Flags: []internal.Parameter{
			{Name: "verbose", Identifiers: []string{"-v"}},
			{Name: "verbose", Identifiers: []string{"-w"}},
		},
	}
	mustErr(t, internal.Validate(s), "duplicate parameter name")
}

func TestValidate_duplicateFlagIdentifiers(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Inputs = &internal.Inputs{
		Flags: []internal.Parameter{
			{Name: "verbose", Identifiers: []string{"-v"}},
			{Name: "vector", Identifiers: []string{"-v"}},
		},
	}
	mustErr(t, internal.Validate(s), "already used")
}

func TestValidate_invalidFlagIdentifierPattern(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Inputs = &internal.Inputs{
		Flags: []internal.Parameter{
			{Name: "bad", Identifiers: []string{"---triple"}},
		},
	}
	mustErr(t, internal.Validate(s), "pattern")
}

// =============================================================================
// Semantic — argument variadic must be last
// =============================================================================

func TestValidate_variadicMustBeLast(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{
			Name: "cat",
			Inputs: &internal.Inputs{
				Arguments: []internal.Parameter{
					{Name: "paths", Schema: &internal.FieldSchema{Type: "array"}},
					{Name: "out", Schema: &internal.FieldSchema{Type: "string"}},
				},
			},
		},
	}
	err := internal.Validate(s)
	mustErr(t, err, "variadic")
}

func TestValidate_variadicLastAllowed(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{
			Name: "cat",
			Inputs: &internal.Inputs{
				Arguments: []internal.Parameter{
					{Name: "header", Schema: &internal.FieldSchema{Type: "string"}},
					{Name: "paths", Schema: &internal.FieldSchema{Type: "array"}},
				},
			},
		},
	}
	mustNoErr(t, internal.Validate(s))
}

func TestValidate_duplicateArgNames(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{
			Name: "greet",
			Inputs: &internal.Inputs{
				Arguments: []internal.Parameter{
					{Name: "who", Schema: &internal.FieldSchema{Type: "string"}},
					{Name: "who", Schema: &internal.FieldSchema{Type: "string"}},
				},
			},
		},
	}
	mustErr(t, internal.Validate(s), "duplicate parameter name")
}

// =============================================================================
// Semantic — files (config) references
// =============================================================================

func TestValidate_configFileNameMustExist(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Files = []internal.ConfigSpec{
		{Name: "app", Path: "~/.config/app.yaml", Format: "yaml"},
	}
	s.Inputs = &internal.Inputs{
		Files: []internal.Parameter{
			{Name: "port", Schema: &internal.FieldSchema{File: "nonexistent", Key: "server.port", Type: "int"}},
		},
	}
	mustErr(t, internal.Validate(s), "does not match any declared files")
}

func TestValidate_configFileNameResolves(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Files = []internal.ConfigSpec{
		{Name: "app", Path: "~/.config/app.yaml", Format: "yaml"},
	}
	s.Inputs = &internal.Inputs{
		Files: []internal.Parameter{
			{Name: "port", Schema: &internal.FieldSchema{File: "app", Key: "server.port", Type: "int"}},
		},
	}
	mustNoErr(t, internal.Validate(s))
}

func TestValidate_duplicateConfigFileName(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Files = []internal.ConfigSpec{
		{Name: "app", Path: "/a"},
		{Name: "app", Path: "/b"},
	}
	mustErr(t, internal.Validate(s), "duplicate configuration file name")
}

func TestValidate_invalidConfigFormat(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Files = []internal.ConfigSpec{
		{Name: "app", Path: "/a", Format: "xml"},
	}
	mustErr(t, internal.Validate(s), "xml")
}

// =============================================================================
// Semantic — env-var pattern
// =============================================================================

func TestValidate_envVarPattern(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Inputs = &internal.Inputs{
		Variables: []internal.Parameter{
			{Name: "home", Schema: &internal.FieldSchema{Variable: "lowercase", Type: "string"}},
		},
	}
	mustErr(t, internal.Validate(s), "pattern")
}

// =============================================================================
// Semantic — events
// =============================================================================

func TestValidate_eventNamePattern(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Events = []internal.EventSpec{
		{Name: "UpperCase"},
	}
	mustErr(t, internal.Validate(s), "pattern")
}

func TestValidate_duplicateEventNames(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Events = []internal.EventSpec{
		{Name: "startup"},
		{Name: "startup"},
	}
	mustErr(t, internal.Validate(s), "duplicate event name")
}

// =============================================================================
// Semantic — metadata
// =============================================================================

func TestValidate_metadataVarPattern(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Metadata = []internal.MetadataEntry{
		{Var: "lowercase"},
	}
	mustErr(t, internal.Validate(s), "exported Go identifier")
}

func TestValidate_duplicateMetadataVars(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Metadata = []internal.MetadataEntry{
		{Var: "Version"},
		{Var: "Version"},
	}
	mustErr(t, internal.Validate(s), "duplicate metadata var")
}

// =============================================================================
// Semantic — $ref resolution
// =============================================================================

func TestValidate_refResolves(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Schemas = map[string]*internal.FieldSchema{
		"MyType": {Type: "string"},
	}
	s.Inputs = &internal.Inputs{
		Flags: []internal.Parameter{
			{
				Name:        "x",
				Identifiers: []string{"-x"},
				Schema:      &internal.FieldSchema{Ref: "#/schemas/MyType"},
			},
		},
	}
	mustNoErr(t, internal.Validate(s))
}

func TestValidate_refMissing(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Schemas = map[string]*internal.FieldSchema{
		"Other": {Type: "string"},
	}
	s.Inputs = &internal.Inputs{
		Flags: []internal.Parameter{
			{
				Name:        "x",
				Identifiers: []string{"-x"},
				Schema:      &internal.FieldSchema{Ref: "#/schemas/Missing"},
			},
		},
	}
	mustErr(t, internal.Validate(s), "$ref")
}

// =============================================================================
// SpecError shape
// =============================================================================

func TestValidate_aggregatesMultipleErrors(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{Name: "list"},
		{Name: "list"}, // duplicate
		{
			Name: "show",
			Inputs: &internal.Inputs{
				Flags: []internal.Parameter{
					{Name: "v", Identifiers: []string{"-v"}},
					{Name: "v", Identifiers: []string{"-w"}}, // duplicate name
				},
			},
		},
	}
	err := internal.Validate(s)
	if err == nil {
		t.Fatal("expected aggregated error, got nil")
	}
	var spec *internal.SpecError
	if !errors.As(err, &spec) {
		t.Fatalf("error is not *SpecError: %T", err)
	}
	if len(spec.Issues) < 2 {
		t.Errorf("Issues: got %d, want at least 2", len(spec.Issues))
	}
}

func TestValidate_errorIsSentinel(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Name = ""
	err := internal.Validate(s)
	if !errors.Is(err, internal.ErrInvalidSpec) {
		t.Errorf("errors.Is(err, ErrInvalidSpec) = false; want true")
	}
}

func TestValidate_errorAsSpecError(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Name = ""
	err := internal.Validate(s)
	var spec *internal.SpecError
	if !errors.As(err, &spec) {
		t.Fatalf("errors.As failed; err type: %T", err)
	}
	if len(spec.Issues) == 0 {
		t.Errorf("SpecError has no issues")
	}
}

func TestSpecIssue_ErrorFormatting(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		issue   internal.SpecIssue
		wantSub string
	}{
		{"loc+kw", internal.SpecIssue{Location: "x", Keyword: "k", Message: "m"}, "[x] (k) m"},
		{"loc only", internal.SpecIssue{Location: "x", Message: "m"}, "[x] m"},
		{"kw only", internal.SpecIssue{Keyword: "k", Message: "m"}, "(k) m"},
		{"msg only", internal.SpecIssue{Message: "m"}, "m"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.issue.Error(); got != tc.wantSub {
				t.Errorf("Error: got %q, want %q", got, tc.wantSub)
			}
		})
	}
}

// =============================================================================
// JSON pointer → dotted form
// =============================================================================

// These tests reach in via the public Validate path: failing schema
// constraints produce InstanceLocation pointers that the validator
// dot-format converts before surfacing. We assert by inspecting the
// emitted Issues directly.
func TestValidate_dottedLocationConversion(t *testing.T) {
	t.Parallel()
	s := validSpec()
	s.Commands = []internal.Command{
		{Name: "bad name"}, // fails the schema pattern; instance location is /commands/0/name
	}
	err := internal.Validate(s)
	var spec *internal.SpecError
	if !errors.As(err, &spec) {
		t.Fatalf("not a SpecError: %T", err)
	}
	hasDotted := false
	for _, iss := range spec.Issues {
		if strings.Contains(iss.Location, "commands[0].name") {
			hasDotted = true
		}
	}
	if !hasDotted {
		// Print the issues to aid debugging.
		var b strings.Builder
		for _, iss := range spec.Issues {
			b.WriteString(iss.Error())
			b.WriteString("\n")
		}
		t.Errorf("no issue with dotted commands[0].name location.\nissues:\n%s", b.String())
	}
}

// =============================================================================
// Conf validation
// =============================================================================

func TestValidateConf_nilOK(t *testing.T) {
	t.Parallel()
	if err := internal.ValidateConf(nil); err != nil {
		t.Errorf("ValidateConf(nil): got %v, want nil", err)
	}
}

func TestValidateConf_minimalOK(t *testing.T) {
	t.Parallel()
	c := &internal.Conf{
		SchemaURL: "https://raw.githubusercontent.com/go-rotini/rotini/refs/tags/1.2.3/schemas/conf.json",
	}
	mustNoErr(t, internal.ValidateConf(c))
}

func TestValidateConf_missingSchemaURL(t *testing.T) {
	t.Parallel()
	c := &internal.Conf{}
	mustErr(t, internal.ValidateConf(c), "$schema")
}

// =============================================================================
// FieldSchema.RequiredBool / RequiredFields
// =============================================================================

func TestFieldSchema_RequiredBool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"true", "true", true},
		{"false", "false", false},
		{"absent", "", false},
		{"array (not a bool)", `["a"]`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var s internal.FieldSchema
			if tc.raw != "" {
				s.Required = json.RawMessage(tc.raw)
			}
			if got := s.RequiredBool(); got != tc.want {
				t.Errorf("RequiredBool: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFieldSchema_RequiredFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"two", `["a","b"]`, []string{"a", "b"}},
		{"empty", `[]`, nil},
		{"bool (not an array)", `true`, nil},
		{"absent", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var s internal.FieldSchema
			if tc.raw != "" {
				s.Required = json.RawMessage(tc.raw)
			}
			got := s.RequiredFields()
			if len(got) != len(tc.want) {
				t.Errorf("RequiredFields: got %v, want %v", got, tc.want)
				return
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("RequiredFields[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}
