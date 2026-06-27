package rotini

import "embed"

// Source embeds the runtime package's own Go source so the rotini codegen tool can
// emit it into a user's project (each file's `package rotini` clause rewritten to the
// target package). This file (embed.go) and the runtime's *_test.go files are filtered
// out at emit time — they are codegen support / in-repo tests, not runtime behavior.
//
//go:embed *.go
var Source embed.FS
