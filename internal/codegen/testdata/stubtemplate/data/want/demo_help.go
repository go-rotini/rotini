package demo

// Package: "demo"
// HandlerType: "demoHelpHandler"
// InputsType: "DemoHelpInputs"
// Invocation: "demo help"
// Prefix: "DemoHelp"
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
// HelpPathArg: "Command"
// HelpPlugins: false
// VersionOnly: false
// CompletionArg: ""
// PrintHelpWhenBare: false
// NeedsInputs: true
// UsesInputs: true
// Redact: false

var _ rotini.Handler = (*demoHelpHandler)(nil)

type demoHelpHandler struct{}
