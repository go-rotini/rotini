package internal

import (
	"errors"
	"fmt"
	"sync"

	"github.com/go-rotini/jsonschema"
	"github.com/go-rotini/rotini"
)

// errSpecPathRequired is reported by [Validate] when no spec-file path is
// supplied.
var errSpecPathRequired = errors.New("spec file path is required")

// The embedded rotini JSON Schemas are immutable, so each is compiled at
// most once per process and the result cached.
var (
	specSchemaOnce sync.Once
	specSchema     *jsonschema.Schema
	errSpecSchema  error

	confSchemaOnce sync.Once
	confSchema     *jsonschema.Schema
	errConfSchema  error
)

// violationError is a single schema-validation failure: the location of the
// offending value within the document and a human-readable message, tagged
// by document kind ("spec" or "conf").
type violationError struct {
	kind string
	loc  string
	msg  string
}

func (e *violationError) Error() string {
	return fmt.Sprintf("%s: %s: %s", e.kind, e.loc, e.msg)
}

// loadSpecSchema compiles the embedded spec JSON Schema once and returns
// the cached result.
func loadSpecSchema() (*jsonschema.Schema, error) {
	specSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(rotini.SchemaSpec)
		if err != nil {
			errSpecSchema = fmt.Errorf("compile spec schema: %w", err)
			return
		}
		specSchema = s
	})
	return specSchema, errSpecSchema
}

// loadConfSchema compiles the embedded conf JSON Schema once and returns
// the cached result.
func loadConfSchema() (*jsonschema.Schema, error) {
	confSchemaOnce.Do(func() {
		s, err := jsonschema.Compile(rotini.SchemaConf)
		if err != nil {
			errConfSchema = fmt.Errorf("compile conf schema: %w", err)
			return
		}
		confSchema = s
	})
	return confSchema, errConfSchema
}

// Validate checks a rotini spec file, and optionally a conf file, against
// the embedded rotini JSON Schemas.
//
// specPath is the spec-file command argument and is required. confPath is
// the value of the -c/--config flag; pass "" when no conf file was given.
// The rotini handler parses those flags and passes their values here.
//
// Every problem found — a missing or unreadable file, a format-conversion
// failure, or an individual schema violation — is aggregated and returned
// as a single error via [errors.Join]; the calling handler unwraps it for
// display. Validate returns nil when the spec (and conf, if given) are
// valid.
func Validate(specPath, confPath, failMode string) error {
	fast := resolveFailMode(failMode) == "fast"

	var problems []error
	if specPath == "" {
		problems = append(problems, errSpecPathRequired)
	} else {
		problems = append(problems, validateDocument(specPath, "spec", loadSpecSchema)...)
	}
	if fast && len(problems) > 0 {
		return problems[0]
	}
	if confPath != "" {
		problems = append(problems, validateDocument(confPath, "conf", loadConfSchema)...)
	}
	if fast && len(problems) > 0 {
		return problems[0]
	}

	return errors.Join(problems...)
}

// resolveFailMode resolves the validate failure-reporting mode: an explicit value
// (the --fail flag) wins; otherwise the module-root conf's validate.fail is used,
// defaulting to "collect". Only "fast" enables fast mode; anything else collects.
func resolveFailMode(failMode string) string {
	if failMode != "" {
		return failMode
	}
	root, _, err := findModule()
	if err != nil {
		return "collect"
	}
	confPath, err := discoverFile(root, ".rotini.conf.")
	if err != nil {
		return "collect"
	}
	conf, err := ReadConf(confPath)
	if err != nil || conf.Validate == nil {
		return "collect"
	}
	return conf.Validate.Fail
}

// validateDocument reads the document at path, compiles its schema, and
// returns one error per problem: a read/convert failure, a schema-compile
// failure, or one [*violationError] per schema violation. It returns nil
// when the document is valid.
func validateDocument(path, kind string, loadSchema func() (*jsonschema.Schema, error)) []error {
	instance, err := toJSON(path)
	if err != nil {
		return []error{fmt.Errorf("%s file: %w", kind, err)}
	}
	schema, err := loadSchema()
	if err != nil {
		return []error{err}
	}
	result, err := schema.Validate(instance)
	if err != nil {
		return []error{fmt.Errorf("validate %s: %w", kind, err)}
	}
	if result.Valid {
		return nil
	}

	problems := make([]error, 0, len(result.Errors))
	for i := range result.Errors {
		ve := &result.Errors[i]
		loc := ve.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		problems = append(problems, &violationError{kind: kind, loc: loc, msg: ve.Message})
	}
	return problems
}
