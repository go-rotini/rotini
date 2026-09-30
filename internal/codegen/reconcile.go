package codegen

// Reconcile is the first pipeline stage: read and decode the spec and conf into their
// in-memory shapes. The spec is required; the conf is optional and falls back to the default.
// Each reconciled value retains its source format and bytes, so a later validation problem can
// be located back to file:line:col.

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
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

func (s *Spec) normalize() {
	qualifySchemaRefs(reflect.ValueOf(s).Elem())
	inheritScalarRefConstraints(s)
	hoistItemConstraints(s)
	normalizeBounds(s)
}

// normalizeBounds converts a bound written in a measured type's own spelling — `minimum: 1s` on a
// duration, `maximum: 1Gi` on a bytesize — to a number in that type's unit (nanoseconds,
// bytes), which is what the generated constraint and every lint rule compare against. The
// runtime's own parser reads the text, so `1h30m` or `1.5Gi` means here exactly what it means
// on the command line. A string that does not convert stays a string for
// lintConstraintApplicability to report.
func normalizeBounds(s *Spec) {
	walkCommands(s, func(c *Command, _ string) {
		eachInputSchema(c.inputs(), func(_, _ string, schema *InputSchema) {
			if schema == nil {
				return
			}
			elem := strings.TrimPrefix(getSchemaType(schema), "[]")
			for _, b := range []*any{&schema.Minimum, &schema.Maximum, &schema.ExclusiveMinimum, &schema.ExclusiveMaximum, &schema.MultipleOf} {
				if text, ok := (*b).(string); ok {
					if n, ok := measuredValue(elem, text); ok {
						*b = n
					}
					continue
				}
				// A bare number on a duration has no unit — 5 what? It is kept as text so the
				// lint reports it and asks for 5s, rather than reading it as nanoseconds.
				if n := bound(*b); n != nil && elem == "time.Duration" {
					*b = strconv.FormatFloat(*n, 'g', -1, 64)
				}
			}
		})
	})
}

// inheritScalarRefConstraints gives an input that refers to a named SCALAR schema that schema's
// constraints — enum, pattern, bounds, lengths — wherever the input leaves them unset. The $ref
// still names the Go type. Before this, `$ref: Kind` where Kind is {type: string, enum: [...]}
// generated a Kind field and enforced nothing: the enum lived on the named schema, and only the
// input's own keys reached the definition. A named OBJECT schema is left alone; its rules are
// validated as a document (see object-valued flags).
func inheritScalarRefConstraints(s *Spec) {
	named := s.Command.Schemas
	inherit := func(b *BaseSchema) {
		if b == nil || b.Ref == "" {
			return
		}
		src, ok := named[strings.TrimPrefix(b.Ref, "#/schemas/")]
		if !ok || src.Type == "object" || len(src.Properties) > 0 {
			return
		}
		if len(b.Enum) == 0 {
			b.Enum = src.Enum
		}
		if b.Pattern == "" {
			b.Pattern = src.Pattern
		}
		if b.MinLength == 0 {
			b.MinLength = src.MinLength
		}
		if b.MaxLength == 0 {
			b.MaxLength = src.MaxLength
		}
		for _, pair := range [][2]*any{{&b.Minimum, &src.Minimum}, {&b.Maximum, &src.Maximum},
			{&b.ExclusiveMinimum, &src.ExclusiveMinimum}, {&b.ExclusiveMaximum, &src.ExclusiveMaximum},
			{&b.MultipleOf, &src.MultipleOf}} {
			if *pair[0] == nil {
				*pair[0] = *pair[1]
			}
		}
	}
	walkCommands(s, func(c *Command, _ string) {
		eachInputSchema(c.inputs(), func(_, _ string, schema *InputSchema) {
			if schema == nil {
				return
			}
			inherit(&schema.BaseSchema)
			if schema.Items != nil {
				inherit(&schema.Items.BaseSchema)
			}
		})
	})
}

// qualifySchemaRefs rewrites every bare schema reference (`$ref: DB`) to its pointer form
// (`#/schemas/DB`), so everything downstream — codegen, the JSON Schema documents rotini
// assembles, the lint rules — meets one spelling. The bare name is the one people write; the
// pointer is what JSON Schema tooling expects. Only a schema block's $ref is touched: a
// command's $ref names a spec file to compose, and is left alone.
func qualifySchemaRefs(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			qualifySchemaRefs(v.Elem())
		}
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[BaseSchema]() {
			if ref := v.FieldByName("Ref"); ref.String() != "" && !strings.HasPrefix(ref.String(), "#/") {
				ref.SetString("#/schemas/" + ref.String())
			}
		}
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				qualifySchemaRefs(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			qualifySchemaRefs(v.Index(i))
		}
	case reflect.Map:
		// Map values are not addressable: rewrite a copy and store it back.
		for _, k := range v.MapKeys() {
			cp := reflect.New(v.Type().Elem()).Elem()
			cp.Set(v.MapIndex(k))
			qualifySchemaRefs(cp)
			v.SetMapIndex(k, cp)
		}
	}
}

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
