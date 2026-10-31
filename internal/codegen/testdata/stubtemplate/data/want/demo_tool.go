package demo

// Package: "demo"
// HandlerType: "demoToolHandler"
// InputsType: "DemoToolInputs"
// Invocation: "demo tool"
// Prefix: "DemoTool"
// RuntimeImport: "\"github.com/go-rotini/rotini\""
// Header: ""
// ErrorHooks: true
// NewShape: true
// HelpFlag: ""
// HelpFrame: ""
// VersionFlag: "Version"
// RootHook: false
// RootHelpFlag: ""
// RootVersionFlag: ""
// HelpPathArg: ""
// HelpPlugins: false
// VersionOnly: false
// CompletionArg: ""
// PrintHelpWhenBare: false
// NeedsInputs: true
// UsesInputs: true
// Redact: false

var _ rotini.Handler = (*demoToolHandler)(nil)

type demoToolHandler struct{}
