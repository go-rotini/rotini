package demo

// Package: "demo"
// HandlerType: "demoHandler"
// InputsType: "DemoInputs"
// Invocation: "demo"
// Prefix: "Demo"
// RuntimeImport: "\"github.com/go-rotini/rotini\""
// Header: ""
// ErrorHooks: true
// NewShape: true
// HelpFlag: ""
// HelpFrame: ""
// VersionFlag: ""
// RootHook: true
// RootHelpFlag: "Help"
// RootVersionFlag: "Version"
// HelpPathArg: ""
// HelpPlugins: false
// VersionOnly: false
// CompletionArg: ""
// PrintHelpWhenBare: true
// NeedsInputs: true
// UsesInputs: false
// Redact: false

var _ rotini.Handler = (*demoHandler)(nil)

type demoHandler struct{}
