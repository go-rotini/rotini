//go:build !windows

package rotini

// windowsGlobs is nil: outside Windows the shell has already expanded glob arguments. TestMain
// sets it to read argv as Windows does.
var windowsGlobs func(p *parsedInputs, args []ArgDef, idx int)
