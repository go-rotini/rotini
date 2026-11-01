package demo

// Package: "demo"
// HandlerType: "demoVersionHandler"
// InputsType: "DemoVersionInputs"
// Invocation: "demo version"
// Prefix: "DemoVersion"
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
// VersionOnly: true
// CompletionArg: ""
// PrintHelpWhenBare: false
// NeedsInputs: false
// UsesInputs: false
// Redact: false

var _ rotini.Handler = (*demoVersionHandler)(nil)

type demoVersionHandler struct{}
