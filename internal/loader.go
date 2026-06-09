// Package codegen builds the rotini codegen pipeline — loading a spec and its
// conf, validating and linting them, and generating (or scaffolding) the program.
package internal

import (
	"errors"
	"fmt"
	"os"
)

// errSpecPathRequired is reported by LoadSpec when no spec path is given (and, in
// a later increment, no default-location spec is found).
var errSpecPathRequired = errors.New("a spec file path is required")

// Overrides are the flag-derived conf settings the Loader overlays onto the
// resolved conf, so every downstream stage reads a single, final Conf rather than
// reconciling flags against file contents itself.
type Overrides struct {
	FailMode string // --fail; overrides conf.validate.fail when non-empty
}

// applyTo overlays the non-empty overrides onto conf in place.
func (o Overrides) applyTo(conf *Conf) {
	if o.FailMode != "" {
		if conf.Validate == nil {
			conf.Validate = &ValidateConfig{}
		}
		conf.Validate.Fail = o.FailMode
	}
}

// Input is a loaded rotini document — its resolved path and decoded value. The
// Loader produces one per document: an Input[internal.Spec] and an
// Input[internal.Conf]. The zero value (empty Path, nil Doc) represents "not
// present" — e.g. an optional conf that wasn't found, where defaults are used.
type Input[T any] struct {
	Path string // resolved path to the document; "" when none was found
	Doc  *T     // the decoded document (with defaults applied / overrides overlaid, for conf)
}

// Loader is the first stage of every codegen workflow: it resolves and reads a
// rotini spec and its conf into a typed model — finding the files at their default
// locations when explicit paths aren't given, applying the built-in conf defaults,
// and overlaying any flag-derived overrides.
type Loader struct {
}

// NewLoader constructs a Loader with the default file-resolution policy.
func NewLoader() *Loader {
	return &Loader{}
}

// LoadSpec resolves and reads the spec file into an Input. The spec is required.
//
// For now it reads the given path directly; a later increment adds the
// default-location fallback (discovering a .rotini.spec.* when path is empty).
func (l *Loader) LoadSpec(path string) (Input[Spec], error) {
	if path == "" {
		return Input[Spec]{}, errSpecPathRequired
	}
	spec, err := ReadSpec(path)
	if err != nil {
		return Input[Spec]{}, err
	}
	return Input[Spec]{Path: path, Doc: spec}, nil
}

// LoadConf resolves and reads the conf into an Input, then overlays the flag
// overrides. The conf is optional: when no path is given and none is discovered
// beside the spec (or the resolved path does not exist), a default Conf is used and
// the returned Input.Path is "".
//
// It takes the already-loaded spec because conf resolution depends on it — the
// spec's path locates a conf alongside it, and (in a later increment) the spec's
// root name drives the built-in package defaults.
func (l *Loader) LoadConf(spec Input[Spec], confPath string, overrides Overrides) (Input[Conf], error) {
	confPath = ResolveConfPath(spec.Path, confPath)

	conf := &Conf{} // default when no conf is present
	if confPath != "" {
		if _, statErr := os.Stat(confPath); statErr == nil {
			c, err := ReadConf(confPath)
			if err != nil {
				return Input[Conf]{}, err
			}
			conf = c
		} else if os.IsNotExist(statErr) {
			confPath = "" // resolved path does not exist → fall back to defaults
		} else {
			return Input[Conf]{}, fmt.Errorf("stat conf %s: %w", confPath, statErr)
		}
	}

	overrides.applyTo(conf)

	// TODO (next increment): apply the built-in conf defaults — the cmd/<root>/cli
	// package paths, gen file names, and feature dirs — keyed off the spec's root
	// name (spec.Doc.Command.Name). Open question: does that package-path defaulting
	// belong here (Loader yields a fully-defaulted Conf) or in the Generator (which
	// is what actually consumes those paths)?

	return Input[Conf]{Path: confPath, Doc: conf}, nil
}
