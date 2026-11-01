package demo

// Package: "demo"
// HandlerType: "demoLegacyHandler"
// InputsType: "DemoLegacyInputs"
// Invocation: "demo legacy"
// Prefix: "DemoLegacy"
// RuntimeImport: "\"github.com/go-rotini/rotini\""
// Header: ""
// ErrorHooks: true
// NewShape: true
// HelpFlag: "Help"
// HelpFrame: "DemoLegacy"
// VersionFlag: ""
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

var _ rotini.Handler = (*demoLegacyHandler)(nil)

type demoLegacyHandler struct{}
