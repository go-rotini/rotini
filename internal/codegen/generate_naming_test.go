package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGeneratedNamesKeepGoInitialisms pins the casing of every name codegen makes up — the names
// a user's handler code types. Go writes initialisms in capitals (APIGroup, URL, IP), and so do
// its linters; generated code used to produce ApiGroup and Url, which a user could not change
// and a linter would flag in their own code that referenced it (rubectl R-34).
func TestGeneratedNamesKeepGoInitialisms(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"api-resources": "APIResources",
		"api-group":     "APIGroup",
		"base-url":      "BaseURL",
		"ip":            "IP",
		"pod-ip":        "PodIP",
		"apiKey":        "APIKey",
		"user_id":       "UserID",
		"http-get":      "HTTPGet",
		"generate":      "Generate",
		"idle":          "Idle", // a word that merely STARTS like one is left alone
		"urls":          "Urls", // plurals are not in the list, as in Go's linters
		"port-forward":  "PortForward",
	} {
		if got := toPascalCase(in); got != want {
			t.Errorf("toPascalCase(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"ApiVersion": "APIVersion",
		"HttpGetUrl": "HTTPGetURL",
		"PodIp":      "PodIP",
		"DB":         "DB",
		"Id":         "ID",
		"Identity":   "Identity",
		"Utf8Name":   "UTF8Name",
	} {
		if got := initialismCase(in); got != want {
			t.Errorf("initialismCase(%q) = %q, want %q", in, got, want)
		}
	}
	// Unexported names — handler types — lower the whole leading initialism.
	for in, want := range map[string]string{
		"APIResources":        "apiResources",
		"URL":                 "url",
		"RubectlAPIResources": "rubectlAPIResources",
		"Rotini":              "rotini",
		"":                    "",
	} {
		if got := lowerFirst(in); got != want {
			t.Errorf("lowerFirst(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCommandStubFilename_underscoresDashes pins Go's file-name convention for the stubs: a
// command's '-' is written '_' (rubectl R-26). The reserved-suffix escape still applies to the
// result, so a dashed name that ENDS in a reserved token after the rewrite is escaped too.
func TestCommandStubFilename_underscoresDashes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct{ root, path, override, want string }{
		{"config", "get-contexts", "", "config_get_contexts.go"},
		{"rubectl", "port-forward", "", "rubectl_port_forward.go"},
		{"acme-cache", "", "", "acme_cache.go"},
		{"app", "run-test", "", "app_run_test_.go"}, // "_test.go" would make it a test file
		{"app", "build-linux", "", "app_build_linux_.go"},
		{"app", "get-contexts", "custom-name.go", "custom-name.go"}, // an explicit filename is verbatim
	} {
		if got := commandStubFilename(tt.root, tt.path, tt.override); got != tt.want {
			t.Errorf("commandStubFilename(%q, %q, %q) = %q, want %q", tt.root, tt.path, tt.override, got, tt.want)
		}
	}
}

// TestStubUnderItsDashedName_isStillTheStub proves a project generated before stub names used
// '_' survives regeneration: its handler file under the old name is neither duplicated by a
// fresh stub under the new name (which fails the build with every type declared twice) nor
// pruned as an orphan (which would delete the user's code).
func TestStubUnderItsDashedName_isStillTheStub(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	c := genCommand{filename: "app_get_thing.go", dashedFilename: "app_get-thing.go"}
	if got := stubFileFor(dir, c); got != "app_get_thing.go" {
		t.Errorf("fresh project: stub = %q, want the underscore name", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "app_get-thing.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stubFileFor(dir, c); got != "app_get-thing.go" {
		t.Errorf("existing dashed stub: stub = %q, want it kept", got)
	}
	if got := dashedStubFilename("app", "get-thing", ""); got != "app_get-thing.go" {
		t.Errorf("dashedStubFilename = %q", got)
	}
	if got := dashedStubFilename("app", "get_thing", ""); got != "" {
		t.Errorf("no dash, no old name: got %q", got)
	}
}
