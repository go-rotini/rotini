package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// This file writes install-ready files: with install_dir set on the completion or man feature,
// generate also writes each shell's script and each man page under that directory, named the
// way packages install them, whether or not the feature embeds its pages.

// completionInstallNames maps each shell to the file name its completion script is installed
// as, for <name> the root command's name.
var completionInstallNames = map[string]func(name string) string{
	"bash":       func(name string) string { return name + ".bash" },
	"zsh":        func(name string) string { return "_" + name },
	"fish":       func(name string) string { return name + ".fish" },
	"powershell": func(name string) string { return name + ".ps1" },
	"nushell":    func(name string) string { return name + ".nu" },
}

// installDir returns the module-root-relative install_dir of the feature, or "".
func (p *program) installDir(feature string) string {
	if p.conf == nil || p.conf.Generate == nil {
		return ""
	}
	if f := p.conf.Generate.featureOf(feature); f != nil && installFeatures[feature] {
		return f.InstallDir
	}
	return ""
}

// emitInstallFiles writes the install-ready files of every enabled feature that sets
// install_dir: completions/<file> per shell, and man/man<section>/<page>.<section> per listed
// man page. Each holds exactly what the generated accessor returns.
func (p *program) emitInstallFiles() error {
	for _, o := range p.featureOutputs {
		dir := p.installDir(o.desc.name)
		if dir == "" {
			continue
		}
		abs, err := underModule(p.module.root, "generate.features."+o.desc.name+".install_dir", dir)
		if err != nil {
			return err
		}
		for i, n := range o.nodes {
			var rel string
			switch {
			case o.desc.perShell:
				name, ok := completionInstallNames[n.name]
				if !ok {
					continue
				}
				rel = filepath.Join("completions", name(p.rootName))
			case o.desc.manPages:
				if !n.listed {
					continue
				}
				rel = filepath.Join("man", "man"+n.data.Section, n.file)
			default:
				continue
			}
			if err := p.plan.write(filepath.Join(abs, rel), []byte(o.contents[i])); err != nil {
				return fmt.Errorf("write %s install file %s: %w", o.desc.name, filepath.ToSlash(rel), err)
			}
		}
	}
	return nil
}

// pruneInstallFiles removes install files this run no longer produces. Under the man
// feature's install_dir that is a page for a removed command or topic, a command that became
// hidden, every page of an old section, every page the previous generate listed under an old
// root name, and every page when the feature is off. Only the root's own pages (<root>.N,
// <root>-*.N) and the pages the previous cmd file listed are candidates. Completion files are
// one per shell, so they go stale only when the completion feature is turned off.
func (p *program) pruneInstallFiles() error {
	if p.skipPrune {
		return nil
	}
	if err := p.pruneInstalledMan(); err != nil {
		return err
	}
	return p.pruneInstalledCompletion()
}

// pruneInstalledMan removes the stale man pages under the man feature's install_dir.
func (p *program) pruneInstalledMan() error {
	dir := p.installDir("man")
	if dir == "" {
		return nil
	}
	o := featureOutput{desc: manFeatureDesc, rootPage: manPageName(p.rootName, nil)}
	enabled := false
	if i := slices.IndexFunc(p.featureOutputs, func(o featureOutput) bool { return o.desc.manPages }); i >= 0 {
		o, enabled = p.featureOutputs[i], true
	}
	abs, err := underModule(p.module.root, "generate.features.man.install_dir", dir)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, n := range o.nodes {
		if n.listed {
			current[filepath.Join("man"+n.data.Section, n.file)] = true
		}
	}
	stale, err := installedManPages(filepath.Join(abs, "man"), o)
	if err != nil {
		return err
	}
	// A page the previous generate listed is rotini's whatever its name.
	if s := p.prior.manSection; s != "" {
		for _, name := range p.prior.manPages {
			stale = append(stale, filepath.Join("man"+s, name+"."+s))
		}
	}
	for _, rel := range stale {
		if current[rel] {
			continue
		}
		path := filepath.Join(abs, "man", rel)
		exists, err := p.plan.exists(path)
		if err == nil && exists {
			err = p.plan.remove(path)
		}
		if err != nil {
			return fmt.Errorf("remove a stale installed man page: %w", err)
		}
		if exists && enabled {
			p.pruned = append(p.pruned, path)
		}
	}
	return nil
}

// installedManPages lists the man<N>/<page> paths under manDir that o owns.
func installedManPages(manDir string, o featureOutput) ([]string, error) {
	var out []string
	for s := '1'; s <= '9'; s++ {
		section := "man" + string(s)
		entries, err := os.ReadDir(filepath.Join(manDir, section))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read the man install directory: %w", err)
		}
		for _, e := range entries {
			if !e.IsDir() && o.owns(e.Name()) {
				out = append(out, filepath.Join(section, e.Name()))
			}
		}
	}
	return out, nil
}

// pruneInstalledCompletion removes the installed completion scripts this run doesn't write:
// those named after the previous root after a rename, and all of them when the completion
// feature is turned off but still names an install_dir.
func (p *program) pruneInstalledCompletion() error {
	dir := p.installDir("completion")
	if dir == "" {
		return nil
	}
	var stale []string
	if !slices.ContainsFunc(p.featureOutputs, func(o featureOutput) bool { return o.desc.perShell }) {
		stale = append(stale, p.rootName)
	}
	if p.prior.root != "" && !strings.EqualFold(p.prior.root, p.rootName) {
		stale = append(stale, p.prior.root)
	}
	if len(stale) == 0 {
		return nil
	}
	abs, err := underModule(p.module.root, "generate.features.completion.install_dir", dir)
	if err != nil {
		return err
	}
	for _, root := range stale {
		for _, shell := range completionShells {
			if err := p.plan.remove(filepath.Join(abs, "completions", completionInstallNames[shell](root))); err != nil {
				return fmt.Errorf("remove a stale installed completion script: %w", err)
			}
		}
	}
	return nil
}
