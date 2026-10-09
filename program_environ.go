package rotini

// WithEnviron sets the environment a run reads, in exec.Cmd.Env's KEY=value form: env
// inputs, flag env fallbacks, config_source variables, $VAR and ~ in configuration paths, the
// XDG and home directories, plugin lookup and the plugin's own environment, and the completion
// messages switch. nil (the default) reads the process environment live; a non-nil slice, even
// an empty one, is the whole environment. Later duplicates win; on Windows names match
// case-insensitively.
//
// Each run reads it through [Context.LookupEnv] and [Context.Environ], so tests can run in
// parallel and one process can run programs with different environments:
//
//	code, err := cmd.NewProgram(cmd.Handlers()).
//		WithEnviron([]string{"HOME=" + home, "APP_TOKEN=t"}).
//		WithDir(t.TempDir()).
//		Run([]string{"deploy"})
//
// A custom source in [InputSettings] reads where it reads; a handler reads the run's
// environment with [Context.LookupEnv].
func (p *Program) WithEnviron(env []string) *Program {
	p.view = p.view.withEnviron(env)
	return p
}

// WithDir sets the directory a run resolves relative paths against: walk-up configuration
// discovery, relative configuration paths, `@file` values, existingfile and existingdir
// checks, and the directory plugins run in. "" (the default) is the process working
// directory. A relative dir is made absolute when the run starts.
//
// Rotini can't change the working directory for one run, so handlers still run in the
// process's. A handler that opens a relative path joins it first:
//
//	f, err := os.Open(filepath.Join(rtx.Dir(), inputs.Deploy.Flags.Manifest))
//
// An existingfile or existingdir value is checked against the run's directory and bound as
// typed, so the handler joins it the same way.
func (p *Program) WithDir(dir string) *Program {
	p.view = p.view.withDir(dir)
	return p
}

// LookupEnv reads one variable of the run's environment ([Program.WithEnviron]): the process
// environment unless one was injected.
func (rtx *Context) LookupEnv(name string) (string, bool) {
	return rtx.osView().lookup(name)
}

// Environ returns a copy of the run's environment ([Program.WithEnviron]) as KEY=value.
func (rtx *Context) Environ() []string {
	return rtx.osView().environ()
}

// Dir returns the run's absolute working directory ([Program.WithDir]): the process working
// directory unless one was injected, or "" when it can't be read.
func (rtx *Context) Dir() string {
	dir, err := rtx.osView().getwd()
	if err != nil {
		return ""
	}
	return dir
}

// WithEnviron sets the environment [Context.Inputs] and its siblings read. See
// [Program.WithEnviron]. It is for a Context built with [NewContextFor]; during a run, the
// change lasts for the rest of that run.
func (rtx *Context) WithEnviron(env []string) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.view = rtx.view.withEnviron(env)
		bindChainView(rtx.chain, rtx.view)
		rtx.mu.Unlock()
	}
	return rtx
}

// WithDir sets the directory relative paths resolve against. See [Program.WithDir]. It is for a
// Context built with [NewContextFor]; during a run, the change lasts for the rest of that run.
// A relative dir is made absolute now.
func (rtx *Context) WithDir(dir string) *Context {
	if rtx != nil {
		rtx.mu.Lock()
		rtx.view = rtx.view.withDir(dir).resolved()
		bindChainView(rtx.chain, rtx.view)
		rtx.mu.Unlock()
	}
	return rtx
}

// osView is the run's environment view; nil (the process) for a nil Context.
func (rtx *Context) osView() *osView {
	if rtx == nil {
		return nil
	}
	rtx.mu.RLock()
	defer rtx.mu.RUnlock()
	return rtx.view
}
