package codegen

// Reconcile is the first pipeline stage: read and decode the spec (required) and conf
// (optional, defaulted) and keep a source locator so later problems can be positioned.

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
)

// reconciledSpec is a decoded spec with its canonical JSON instance (for schema validation)
// and a source locator.
type reconciledSpec struct {
	path   string
	spec   *Spec
	json   []byte
	locate sourceLocator // nil when the format carries no positions
}

// reconciledConf is reconciledSpec for the conf. With no conf file, path is "", conf is the
// default &Conf{}, and json and locate are nil.
type reconciledConf struct {
	path   string
	conf   *Conf
	json   []byte
	locate sourceLocator
}

// reconcileDoc decodes the document at resolved into *T and returns it with its canonical
// JSON instance and a source locator.
func reconcileDoc[T any](resolved string) (doc *T, instance []byte, locate sourceLocator, err error) {
	format, data, err := readRaw(resolved)
	if err != nil {
		return nil, nil, nil, err
	}
	return reconcileData[T](resolved, format, data)
}

// reconcileData is reconcileDoc for a document already in memory, read from resolved.
func reconcileData[T any](resolved string, format fileFormat, data []byte) (doc *T, instance []byte, locate sourceLocator, err error) {
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

// reconcileSpec reads the spec at path, or the first .rotini.spec.* in the working directory
// when path is "". It returns errSpecPathRequired when none is found.
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

// reconcileConf reads the conf at confPath, or the one discovered beside the spec. With none
// found it returns the default &Conf{} with an empty path. An explicitly given path that does
// not exist is an error rather than a silent fallback to defaults.
func reconcileConf(specPath, confPath string) (*reconciledConf, error) {
	rc := &reconciledConf{conf: &Conf{}}

	resolved := resolveConfBesideSpec(specPath, confPath)
	if resolved == "" {
		return rc, nil
	}
	if _, statErr := os.Stat(resolved); statErr != nil {
		if os.IsNotExist(statErr) {
			return nil, fmt.Errorf("conf %s: %w", resolved, os.ErrNotExist)
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

// normalizer is implemented by a decoded document that needs a canonicalizing pass.
// decodeData calls it, so the root spec and every $ref-composed spec reach validation, lint
// and generation in the same normalized form.
type normalizer interface{ normalize() }

func (s *Spec) normalize() {
	qualifySchemaRefs(reflect.ValueOf(s).Elem())
	inheritScalarRefConstraints(s)
	hoistItemConstraints(s)
	normalizeBounds(s)
}

// normalizeBounds converts a bound written in a measured type's spelling (`minimum: 1s` on a
// duration, `maximum: 1Gi` on a bytesize) to a number in that type's unit, parsed by the
// runtime's parser. A string that does not convert is left for lintConstraintApplicability
// to report.
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
				// A unitless number on a duration is turned into text so the lint asks for a
				// unit instead of reading it as nanoseconds.
				if n := bound(*b); n != nil && elem == "time.Duration" {
					*b = strconv.FormatFloat(*n, 'g', -1, 64)
				}
			}
		})
	})
}

// inheritScalarRefConstraints copies a named scalar schema's constraints (enum, pattern,
// bounds, lengths) onto each input, or input items, that $refs it, wherever the input leaves
// them unset. The $ref still names the Go type. Named object schemas are skipped; their values
// are validated as documents.
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
		fillUnsetConstraints(b, &src.BaseSchema)
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

// qualifySchemaRefs rewrites every bare schema-block reference (`$ref: DB`) to pointer form
// (`#/schemas/DB`) so downstream stages see one spelling. A command's $ref, which names a
// spec file, is not a BaseSchema and is left alone.
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

// hoistItemConstraints copies per-value constraints from an array input's `items` onto the
// input itself, where the runtime applies them to every element, so both spellings are
// equivalent:
//
//	schema: { type: array, items: { type: string }, enum: [low, high] }
//	schema: { type: array, items: { type: string, enum: [low, high] } }
//
// An array-level value is never overwritten; lintItemConstraints reports a disagreement. Stdin
// is exempt (its schema has full JSON Schema semantics), as are named schemas and output,
// which are not inputs.
func hoistItemConstraints(spec *Spec) {
	if spec == nil {
		return
	}
	walkCommandsAt(spec, func(c *Command, _, _ string) {
		eachInputSchema(c.inputs(), func(channel, _ string, schema *InputSchema) {
			if channel == "stdin" || schema == nil || schema.Items == nil || !isArrayInputSchema(schema) {
				return
			}
			fillUnsetConstraints(&schema.BaseSchema, &schema.Items.BaseSchema)
		})
	})
}

// fillUnsetConstraints copies src's enum, pattern, lengths and bounds onto dst wherever dst
// leaves them unset. An inherited pattern brings its pattern_message unless dst sets one.
func fillUnsetConstraints(dst, src *BaseSchema) {
	if len(dst.Enum) == 0 {
		dst.Enum = src.Enum
	}
	if dst.Pattern == "" {
		dst.Pattern = src.Pattern
		if dst.PatternMessage == "" {
			dst.PatternMessage = src.PatternMessage
		}
	}
	if dst.MinLength == 0 {
		dst.MinLength = src.MinLength
	}
	if dst.MaxLength == 0 {
		dst.MaxLength = src.MaxLength
	}
	for _, pair := range [][2]*any{{&dst.Minimum, &src.Minimum}, {&dst.Maximum, &src.Maximum},
		{&dst.ExclusiveMinimum, &src.ExclusiveMinimum}, {&dst.ExclusiveMaximum, &src.ExclusiveMaximum},
		{&dst.MultipleOf, &src.MultipleOf}} {
		if *pair[0] == nil {
			*pair[0] = *pair[1]
		}
	}
}

// isArrayInputSchema reports whether an input schema declares a list: `array`, or a Go-style
// `[]T` spelling.
func isArrayInputSchema(schema *InputSchema) bool {
	return schema.Type == "array" || strings.HasPrefix(schema.Type, "[]")
}
