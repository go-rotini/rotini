// Package codegen turns a CLI definition (a .rotini.spec and .rotini.conf) into a Go program.
// It is a small compiler, and the package is laid out as its pipeline.
//
// # Pipeline
//
// The Processor holds rotini's own spec and conf JSON Schemas and runs the author's files
// through four stages:
//
//	reconcile → validate → lint → generate
//
// Each stage in turn:
//
//   - reconcile reads and decodes the spec and conf into the in-memory model.
//   - validate checks the model against rotini's schemas, and its declared version against
//     this binary.
//   - lint applies the rules a JSON Schema cannot express.
//   - generate resolves the model into a program and writes it.
//
// Processor.Generate and Processor.Validate read as the whole process.
//
// # File layout
//
// Each file belongs to one stage, named by its prefix:
//
//	processor*     the Processor and the run/watch loop
//	schema*        rotini's JSON Schemas, the generated model types, reading and walking them
//	reconcile*     reading and decoding the spec and conf, with source positions
//	validate*      schema and version validation, and the problem type
//	lint*          lint rules for the spec and conf, composition, suggestions
//	generate*      the generate stage
//	initialize.go  `rotini initialize`: scaffold a new CLI, then generate it
//
// # Generate
//
// A spec and conf resolve into a [program]: the command tree, the output layout and the target
// module. program.generate runs the emit steps in order:
//
//	emit schemas → emit contract → emit models file → emit cmd file → emit feature outputs →
//	emit handler stubs → emit entrypoint → prune orphans → audit handler hooks
//
// The last step reads the author's handler code instead of writing: it reports methods whose
// names nearly match a lifecycle hook, which the generated `var _ rotini.Handler` assertion
// cannot catch, and handlers that acquire another command's inputs type. The runtime is an
// imported library and is never emitted.
package codegen
