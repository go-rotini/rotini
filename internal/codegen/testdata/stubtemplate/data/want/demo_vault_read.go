package demo

// Package: "demo"
// HandlerType: "demoVaultReadHandler"
// InputsType: "DemoVaultReadInputs"
// Invocation: "demo vault read"
// Prefix: "DemoVaultRead"
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
// CompletionArg: ""
// PrintHelpWhenBare: false
// NeedsInputs: true
// UsesInputs: true
// Redact: false

var _ rotini.Handler = (*demoVaultReadHandler)(nil)

type demoVaultReadHandler struct{}
