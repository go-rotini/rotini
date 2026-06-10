package internal

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rotini/jsonschema"
)

type fileType string

const (
	fileTypeSpec fileType = "spec"
	fileTypeConf fileType = "conf"
)

var (
	//go:embed schema-spec.json
	schemaSpecFileBytes []byte
	//go:embed schema-conf.json
	schemaConfFileBytes []byte
	//go:embed templates/.rotini.spec.yaml.tmpl
	defaultSpecFileBytes []byte
	//go:embed templates/.rotini.conf.yaml.tmpl
	defaultConfFileBytes []byte
)

// getFallbackPaths returns the default discovery locations for a spec or conf file
// within dir, in extension-precedence order.
func getFallbackPaths(dir string, fileType fileType) []string {
	fileExtensions := []string{"yml", "yaml", "toml", "json", "jsonc"}
	paths := make([]string, len(fileExtensions))
	for i, fileExtension := range fileExtensions {
		paths[i] = filepath.Join(dir, fmt.Sprintf(".rotini.%s.%s", fileType, fileExtension))
	}
	return paths
}

// firstExisting returns the first path in paths that exists on disk, or "" if none do.
func firstExisting(paths []string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// specFile holds the compiled spec schema together with the resolved path and decoded
// content of the end-user's spec file — the correct schema and the user's content in
// one value, able to validate itself.
type specFile struct {
	version string             // running binary version, for the $schema guard
	schema  *jsonschema.Schema // compiled spec JSON Schema
	path    string             // resolved spec path
	spec    *Spec              // decoded spec content
}

// NewSpecFile compiles the embedded spec schema, resolves the spec path (the given
// path, else the first .rotini.spec.* in the working directory), and reads + decodes
// the spec. The spec is required: when no path is given and none is discovered it
// returns errSpecPathRequired. version is carried onto the file for the $schema guard.
func NewSpecFile(path, version string) (*specFile, error) {
	schema, err := jsonschema.Compile(schemaSpecFileBytes)
	if err != nil {
		return nil, fmt.Errorf("compile spec schema: %w", err)
	}

	resolved, err := resolveSpecPath(path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, errSpecPathRequired
	}
	spec, err := readSpec(resolved)
	if err != nil {
		return nil, err
	}

	return &specFile{version: version, schema: schema, path: resolved, spec: spec}, nil
}

// validate schema-validates the spec against its compiled schema on the raw JSON
// instance (so unknown-field rules fire), then — only when it is schema-valid — runs
// the rotini-specific rules and enforces the $schema↔version guard. It returns every
// problem found, empty when the spec is valid.
func (f *specFile) validate() []error {
	if problems := validateDocument(f.path, "spec", f.schema); len(problems) > 0 {
		return problems
	}

	var problems []error
	for _, rule := range specLints {
		problems = append(problems, rule(f.spec)...)
	}
	if err := checkSchemaVersion("spec", f.spec.Schema, f.version); err != nil {
		problems = append(problems, err)
	}
	return problems
}

// confFile holds the compiled conf schema together with the resolved path and decoded
// content of the end-user's conf file. The conf is optional: when none is found, path
// is "" and conf is a default &Conf{}.
type confFile struct {
	version string             // running binary version, for the $schema guard
	schema  *jsonschema.Schema // compiled conf JSON Schema
	path    string             // resolved conf path ("" when none — defaults used)
	conf    *Conf              // decoded conf content (or default)
}

// NewConfFile compiles the embedded conf schema, resolves the conf path (the given
// path, else the first .rotini.conf.* beside the spec), and reads + decodes the conf.
// The conf is optional: no path given and none discovered — or a resolved path that
// does not exist — yields a default Conf with an empty path. version is carried onto
// the file for the $schema guard.
func NewConfFile(specPath, confPath, version string) (*confFile, error) {
	schema, err := jsonschema.Compile(schemaConfFileBytes)
	if err != nil {
		return nil, fmt.Errorf("compile conf schema: %w", err)
	}

	f := &confFile{version: version, schema: schema, conf: &Conf{}}

	resolved := resolveConfBesideSpec(specPath, confPath)
	if resolved == "" {
		return f, nil
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		if os.IsNotExist(statErr) {
			return f, nil // optional → default when the resolved path doesn't exist
		}
		return nil, fmt.Errorf("stat conf %s: %w", resolved, statErr)
	}

	conf, err := readConf(resolved)
	if err != nil {
		return nil, err
	}
	f.path, f.conf = resolved, conf
	return f, nil
}

// validate schema-validates the conf against its compiled schema on the raw JSON
// instance when one was resolved, then enforces the $schema↔version guard. A default
// conf (no file) has nothing to validate. It returns every problem found.
func (f *confFile) validate() []error {
	if f.path == "" {
		return nil
	}
	if problems := validateDocument(f.path, "conf", f.schema); len(problems) > 0 {
		return problems
	}
	if err := checkSchemaVersion("conf", f.conf.Schema, f.version); err != nil {
		return []error{err}
	}
	return nil
}
