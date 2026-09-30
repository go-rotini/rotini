package codegen

// Reconcile is the first pipeline stage: read and decode the spec and conf into their
// in-memory shapes. The spec is required; the conf is optional and falls back to the default.
// Each reconciled value retains its source format and bytes, so a later validation problem can
// be located back to file:line:col.

import (
	"fmt"
	"os"
	"strings"
)

// reconciledSpec is an end-user spec read + decoded, alongside the canonical-JSON
// instance (the same bytes generation consumes, for schema validation) and a locator
// from the source format.
type reconciledSpec struct {
	path   string        // resolved spec path
	spec   *Spec         // decoded spec
	json   []byte        // the spec as canonical JSON, for schema validation
	locate sourceLocator // JSON-pointer → source line:col (nil when the format carries no positions)
}

// reconciledConf is reconciledSpec for the conf. The conf is OPTIONAL: when no file is
// found, path is "", conf is the default &Conf{}, and json is nil (nothing to validate).
type reconciledConf struct {
	path   string        // resolved conf path ("" when none — defaults used)
	conf   *Conf         // decoded conf, or the default
	json   []byte        // the conf as canonical JSON (nil when defaulted)
	locate sourceLocator // JSON-pointer → source line:col (nil when no file / no positions)
}

// reconcileDoc is the shared read tail: it decodes the document at resolved into *T, plus its
// canonical-JSON instance for schema validation and a source locator. reconcileSpec and
// reconcileConf differ only in the decoded type and their required-vs-optional path handling.
func reconcileDoc[T any](resolved string) (doc *T, instance []byte, locate sourceLocator, err error) {
	format, data, err := readRaw(resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	doc, err = decodeData[T](format, data, resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	instance, err = bytesToJSON(format, data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", resolved, err)
	}
	return doc, instance, newSourceLocator(format, data), nil
}

// reconcileSpec reads + decodes the end-user spec at path (or the first .rotini.spec.* in
// the working directory when path is ""). The spec is REQUIRED: with no path and none
// discovered it returns errSpecPathRequired.
func reconcileSpec(path string) (*reconciledSpec, error) {
	resolved, err := resolveSpecPath(path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, errSpecPathRequired
	}
	spec, instance, locate, err := reconcileDoc[Spec](resolved)
	if err != nil {
		return nil, err
	}
	return &reconciledSpec{path: resolved, spec: spec, json: instance, locate: locate}, nil
}

// reconcileConf reads + decodes the end-user conf beside the spec (or at confPath). The
// conf is OPTIONAL: no path given and none discovered — or a resolved path that does not
// exist — yields the DEFAULT &Conf{} with an empty path.
func reconcileConf(specPath, confPath string) (*reconciledConf, error) {
	rc := &reconciledConf{conf: &Conf{}}

	resolved := resolveConfBesideSpec(specPath, confPath)
	if resolved == "" {
		return rc, nil
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		if os.IsNotExist(statErr) {
			return rc, nil // optional → default when the resolved path doesn't exist
		}
		return nil, fmt.Errorf("stat conf %s: %w", resolved, statErr)
	}

	conf, instance, locate, err := reconcileDoc[Conf](resolved)
	if err != nil {
		return nil, err
	}
	rc.path, rc.conf, rc.json, rc.locate = resolved, conf, instance, locate
	return rc, nil
}

// normalizer is implemented by a decoded document that needs a canonicalizing pass before any
// later stage sees it. decodeData calls it, so every path that decodes a spec — the root one and
// each spec a `$ref` composes in — hands validation, lint and generation the same model.
type normalizer interface{ normalize() }

func (s *Spec) normalize() { hoistItemConstraints(s) }

// hoistItemConstraints copies per-value constraints declared on an array input's `items` up onto
// the input itself, where the runtime already applies them to every element.
//
// Before this, only the array-level spelling did anything:
//
//	schema: { type: array, items: { type: string }, enum: [low, high] }   // enforced
//	schema: { type: array, items: { type: string, enum: [low, high] } }   // silently ignored
//
// The second is where JSON Schema puts an element constraint, and rotini's schema borrows JSON
// Schema's vocabulary — so the natural spelling was the one that accepted `--level BOGUS`. Both
// spellings now mean the same thing.
//
// An array-level value is never overwritten: when both are set and disagree, lintItemConstraints
// reports it rather than one silently winning. stdin is exempt — its schema validates the piped
// document with full JSON Schema semantics, where `items` constraints are already real — and so
// are the document-level `schemas:` and `output:`, which are not inputs.
func hoistItemConstraints(spec *Spec) {
	if spec == nil {
		return
	}
	walkCommandsAt(spec, func(c *Command, _, _ string) {
		eachInputSchema(c.inputs(), func(channel, _ string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || schema.Items == nil || !isArrayInputSchema(schema) {
				return
			}
			it := &schema.Items.BaseSchema
			if len(schema.Enum) == 0 {
				schema.Enum = it.Enum
			}
			if schema.Pattern == "" {
				schema.Pattern = it.Pattern
			}
			if schema.Minimum == nil {
				schema.Minimum = it.Minimum
			}
			if schema.Maximum == nil {
				schema.Maximum = it.Maximum
			}
			if schema.ExclusiveMinimum == nil {
				schema.ExclusiveMinimum = it.ExclusiveMinimum
			}
			if schema.ExclusiveMaximum == nil {
				schema.ExclusiveMaximum = it.ExclusiveMaximum
			}
			if schema.MultipleOf == nil {
				schema.MultipleOf = it.MultipleOf
			}
			if schema.MinLength == 0 {
				schema.MinLength = it.MinLength
			}
			if schema.MaxLength == 0 {
				schema.MaxLength = it.MaxLength
			}
		})
	})
}

// isArrayInputSchema reports whether an input schema declares a list: `array`, or a Go-style
// `[]T` spelling.
func isArrayInputSchema(schema *InputSchema) bool {
	return schema.Type == "array" || strings.HasPrefix(schema.Type, "[]")
}
