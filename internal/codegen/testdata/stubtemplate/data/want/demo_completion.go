package demo

// Package: "demo"
// HandlerType: "demoCompletionHandler"
// InputsType: "DemoCompletionInputs"
// Invocation: "demo completion"
// Prefix: "DemoCompletion"
// RuntimeImport: "\"github.com/go-rotini/rotini\""
// Header: ""
// ErrorHooks: true
// NewShape: true
// HelpFlag: ""
// HelpFrame: ""
// VersionFlag: ""
// RootHook: false
// RootHelpFlag: ""
// RootVersionFlag: ""
// HelpPathArg: ""
// HelpPlugins: false
// VersionOnly: false
// CompletionArg: "Shell"
// PrintHelpWhenBare: false
// NeedsInputs: true
// UsesInputs: true
// Redact: false

var _ rotini.Handler = (*demoCompletionHandler)(nil)

type demoCompletionHandler struct{}
