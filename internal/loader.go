package internal

// This file owns loading and compiling the embedded rotini JSON Schemas, and
// the specLoader/confLoader pairs that hold a compiled schema together with the
// resolved path and decoded content of the end-user's document — the correct
// schema and the user's content in one value, able to validate itself (the
// validate() methods live in validator.go). File reading and path discovery
// live in reader.go.

import (
	_ "embed"
	"fmt"
	"os"
	"sync"

	"github.com/go-rotini/jsonschema"
)

var (
	//go:embed schema-spec.json
	schemaSpecFileBytes []byte
	//go:embed schema-conf.json
	schemaConfFileBytes []byte
)

// The embedded rotini JSON Schemas are immutable, so each is compiled at most once
// per process and the result cached — the cache spares the recompile when a fresh
// specLoader/confLoader is built per pass (e.g. watch mode rebuilds one on every change).
var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	specSchemaErr  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	confSchemaErr  error
)

// loadSpecSchema compiles the embedded spec schema once and returns the cached result.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() { specSchema, specSchemaErr = jsonschema.Compile(schemaSpecFileBytes) })
	return specSchema, specSchemaErr
}

// loadConfSchema compiles the embedded conf schema once and returns the cached result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() { confSchema, confSchemaErr = jsonschema.Compile(schemaConfFileBytes) })
	return confSchema, confSchemaErr
}

// specLoader holds the compiled spec schema together with the resolved path and decoded
// content of the end-user's spec file.
type specLoader struct {
	version string             // running binary version, for the $schema guard
	schema  *jsonschema.Schema // compiled spec JSON Schema
	path    string             // resolved spec path
	spec    *Spec              // decoded spec content
}

// newSpecLoader resolves the spec path (the given path, else the first .rotini.spec.* in
// the working directory) and reads + decodes the spec, holding it alongside the
// (cached) compiled spec schema. The spec is required: when no path is given and none
// is discovered it returns errSpecPathRequired. version is carried for the $schema guard.
func newSpecLoader(path, version string) (*specLoader, error) {
	schema, err := loadSpecSchema()
	if err != nil {
		return nil, err
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

	return &specLoader{version: version, schema: schema, path: resolved, spec: spec}, nil
}

// confLoader holds the compiled conf schema together with the resolved path and decoded
// content of the end-user's conf file. The conf is optional: when none is found, path
// is "" and conf is a default &Conf{}.
type confLoader struct {
	version string             // running binary version, for the $schema guard
	schema  *jsonschema.Schema // compiled conf JSON Schema
	path    string             // resolved conf path ("" when none — defaults used)
	conf    *Conf              // decoded conf content (or default)
}

// newConfLoader resolves the conf path (the given path, else the first .rotini.conf.*
// beside the spec) and reads + decodes the conf, holding it alongside the (cached)
// compiled conf schema. The conf is optional: no path given and none discovered — or a
// resolved path that does not exist — yields a default Conf with an empty path.
// version is carried for the $schema guard.
func newConfLoader(specPath, confPath, version string) (*confLoader, error) {
	schema, err := loadConfSchema()
	if err != nil {
		return nil, err
	}

	f := &confLoader{version: version, schema: schema, conf: &Conf{}}

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
