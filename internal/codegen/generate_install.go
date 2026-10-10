package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// pruneInstallFiles removes man pages under the man feature's install_dir that this run no
// longer produces: a removed command or topic, a command that became hidden, or every page of
// an old section. Only the root's own pages (<root>.N, <root>-*.N) are candidates. Completion
// files are one per shell, so nothing there goes stale.
func (p *program) pruneInstallFiles() error {
	if p.skipPrune {
		return nil
	}
	dir := p.installDir("man")
	if dir == "" {
		return nil
	}
	i := slices.IndexFunc(p.featureOutputs, func(o featureOutput) bool { return o.desc.manPages })
	if i < 0 {
		return nil
	}
	o := p.featureOutputs[i]
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
	for s := '1'; s <= '9'; s++ {
		section := "man" + string(s)
		entries, err := os.ReadDir(filepath.Join(abs, "man", section))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read the man install directory: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() || !o.owns(e.Name()) || current[filepath.Join(section, e.Name())] {
				continue
			}
			path := filepath.Join(abs, "man", section, e.Name())
			if err := p.plan.remove(path); err != nil {
				return fmt.Errorf("remove a stale installed man page: %w", err)
			}
			p.pruned = append(p.pruned, path)
		}
	}
	return nil
}
