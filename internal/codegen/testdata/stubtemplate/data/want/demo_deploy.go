package demo

// Package: "demo"
// HandlerType: "demoDeployHandler"
// InputsType: "DemoDeployInputs"
// Invocation: "demo deploy"
// Prefix: "DemoDeploy"
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
// UsesInputs: false
// Redact: true

var _ rotini.Handler = (*demoDeployHandler)(nil)

type demoDeployHandler struct{}
