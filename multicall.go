package rotini

import (
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// MulticallDef is how a program dispatches on the name its binary was invoked as (the spec's
// root `multicall`). The zero value is busybox style: a binary linked as `ls` runs the `ls`
// command, as if the user had typed `<root> ls`. The name is the base name of argv[0], never a
// resolved symlink, and on Windows a trailing .exe is dropped and the match ignores case. A
// name that matches no command, the root's own name included, runs the root as usual.
type MulticallDef struct {
	// Prefix is stripped from the invoked name before it is matched against the root's
	// commands: with "acme-", acme-ls runs `ls`. A name without the prefix runs the root.
	Prefix string
	// Complete answers shell completion for the root when the invoked name starts with it
	// (kubectl's "kubectl_complete-"): the arguments are the words to complete, answered in the
	// format [Program.WithCompletion] sets, else [PluginCompletion].
	Complete string
}

// multicallArgv applies def's multicall rule to a run invoked as argv0 with argv. It returns
// the argv to run, with the routed command's name in front, and whether the run is a
// completion request instead. goos is runtime.GOOS, a parameter for tests.
func multicallArgv(def Definition, argv0 string, argv []string, goos string) (routed []string, complete bool) {
	mc := def.Multicall
	if mc == nil || argv0 == "" {
		return argv, false
	}
	windows := goos == "windows"
	name := filepath.Base(argv0)
	if windows {
		name = strings.ReplaceAll(argv0, "/", `\`)
		name = name[strings.LastIndexByte(name, '\\')+1:]
		if ext := filepath.Ext(name); strings.EqualFold(ext, ".exe") {
			name = strings.TrimSuffix(name, ext)
		}
	}
	if mc.Complete != "" && hasPrefixFold(name, mc.Complete, windows) {
		return argv, true
	}
	if equalFold(name, def.Name, windows) {
		return argv, false // the root's own name runs the root, whatever the prefix
	}
	if mc.Prefix != "" {
		if !hasPrefixFold(name, mc.Prefix, windows) {
			return argv, false
		}
		name = name[len(mc.Prefix):]
	}
	if name == "" || equalFold(name, def.Name, windows) {
		return argv, false
	}
	if token, ok := multicallToken(rootFrame(def), name, windows); ok {
		return append([]string{token}, argv...), false
	}
	return argv, false
}

// multicallToken returns the spelling of root's command or declared plugin that name
// invokes: a name, alias or hidden alias, matched without regard to case on Windows.
func multicallToken(root Command, name string, windows bool) (string, bool) {
	if _, ok := findChild(root, name); ok {
		return name, true
	}
	if _, ok := findPlugin(root, name); ok {
		return name, true
	}
	if !windows {
		return "", false
	}
	var spellings []string
	for _, c := range root.Commands {
		spellings = append(append(append(spellings, c.Name), c.Aliases...), c.HiddenAliases...)
	}
	for _, pl := range root.Plugins {
		spellings = append(append(spellings, pl.Name), pl.Aliases...)
	}
	if i := slices.IndexFunc(spellings, func(n string) bool { return strings.EqualFold(n, name) }); i >= 0 {
		return spellings[i], true
	}
	return "", false
}

func hasPrefixFold(s, prefix string, fold bool) bool {
	if fold {
		return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
	}
	return strings.HasPrefix(s, prefix)
}

func equalFold(a, b string, fold bool) bool {
	if fold {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// multicall applies the program's multicall rule to argv; see [MulticallDef].
func (p *Program) multicall(argv []string) (routed []string, complete bool) {
	if p.def.Multicall == nil {
		return argv, false
	}
	return multicallArgv(p.def, p.argv0, argv, runtime.GOOS)
}
