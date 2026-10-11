package rotini

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Plugins wires declared plugins and plugin discovery: running a plugin, finding it on the
// PATH, discovering plugins and completing their names.
func Plugins() Feature {
	return Feature{name: "plugins", apply: func(s *featureSet) {
		s.plugins = &pluginHooks{
			exec: func(p *Program, ctx context.Context, rtx *Context, chain []Command, d *PluginDispatch) (int, error) {
				return p.execPlugin(ctx, rtx, chain, d)
			},
			lookPath: func(name string) (string, bool) {
				p, err := exec.LookPath(name)
				return p, err == nil
			},
			discover: discoverPlugins,
		}
	}}
}

// ResponseFiles wires [Definition.ResponseFiles]: expanding a response file named on the
// command line, in runs and in completion.
func ResponseFiles() Feature {
	return Feature{name: "response-files", apply: func(s *featureSet) {
		s.respFiles = &responseFileHooks{expand: expandResponseFiles, completionWords: completionWords}
	}}
}

// Chdir wires a root flag with the chdir role: changing the working directory, in runs and in
// completion.
func Chdir() Feature {
	return Feature{name: "chdir", apply: func(s *featureSet) {
		s.chdir = &chdirHooks{apply: (*Program).applyChdir, complete: (*Program).completionChdir}
	}}
}

// Multicall wires [Definition.Multicall]: dispatching on the name the program was invoked as.
func Multicall() Feature {
	return Feature{name: "multicall", apply: func(s *featureSet) {
		s.multicall = &multicallHooks{route: multicallArgv}
	}}
}

// features is the set each of p's runs has; none for a program built without a constructor.
func (p *Program) features() *featureSet {
	if p.feats == nil {
		return noFeatures
	}
	return p.feats
}

// missingFeatureIn is missingFeature for the command at path.
func missingFeatureIn(path, feature, ctor, subject string) *WiringError {
	we := missingFeature(feature, ctor, subject)
	we.Command = path
	return we
}

// chdir applies the root's chdir flag through the wired hook (see [Program.applyChdir]). A
// program that declares the flag but does not wire [Chdir] gets a [*WiringError].
func (p *Program) chdir(fs *featureSet, rtx *Context, chain []Command, argv []string) ([]string, error) {
	fd, ok := chdirFlag(p.def)
	if !ok {
		return argv, nil
	}
	if fs.chdir == nil {
		return argv, missingFeatureIn(pathOf(chain), "chdir", "Chdir()",
			fmt.Sprintf("flag %s changes the working directory", flagLabel(fd)))
	}
	return fs.chdir.apply(p, rtx, chain, argv)
}

// recordChdirError records a chdir failure: a [*WiringError] is a fault, anything else the
// user's to fix.
func recordChdirError(rtx *Context, err error) {
	if we, ok := errors.AsType[*WiringError](err); ok {
		rtx.recordFault(asFault(we))
		return
	}
	rtx.RecordError(err)
}
