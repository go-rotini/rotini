package demo

// Package: "demo"
// HandlerType: "demoVaultHandler"
// InputsType: "DemoVaultInputs"
// Invocation: "demo vault"
// Prefix: "DemoVault"
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
// PrintHelpWhenBare: true
// NeedsInputs: true
// UsesInputs: false
// Redact: false

var _ rotini.Handler = (*demoVaultHandler)(nil)

type demoVaultHandler struct{}
