// Package schema holds the canonical rotini JSON Schemas for the
// .rotini.spec and .rotini.conf documents. The schemas are embedded so
// the validator (internal) can reuse them without reading from disk, and
// exposed as public byte slices so other packages — and the published
// raw URL — resolve to the exact same bytes.
//
// schema-spec.json / schema-conf.json in this directory are the single
// source of truth. The co-located embed below is the only way Go can
// embed them (//go:embed cannot reach a parent directory), which is why
// they live here rather than the repo root.
package schemas

import _ "embed"

// Spec is the JSON Schema describing a valid .rotini.spec document,
// embedded from schema-spec.json.
//
//go:embed spec.json
var RotiniSchemaSpec []byte

// Conf is the JSON Schema describing a valid .rotini.conf document,
// embedded from schema-conf.json.
//
//go:embed conf.json
var RotiniSchemaConf []byte
