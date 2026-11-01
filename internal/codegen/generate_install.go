package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// This file writes install-ready files: with install_dir set on the completion, man or markdown
// feature, generate also writes each shell's script, each man page or each markdown page under
// that directory, named the way packages and docs sites use them, whether or not the feature
// embeds its pages.

// completionInstallNames maps each shell to the file name its completion script is installed
// as, for <name> the root command's name.
func completionInstallNames() map[string]func(name string) string {
	return completionInstallNamesOnce.value(func() map[string]func(name string) string {
		return map[string]func(name string) string{
			"bash":       func(name string) string { return name + ".bash" },
			"zsh":        func(name string) string { return "_" + name },
			"fish":       func(name string) string { return name + ".fish" },
			"powershell": func(name string) string { return name + ".ps1" },
			"nushell":    func(name string) string { return name + ".nu" },
		}
	})
}

var completionInstallNamesOnce lazy[map[string]func(name string) string]

// installDir returns the module-root-relative install_dir of the feature, or "".
func (p *program) installDir(feature string) string {
	if p.conf == nil || p.conf.Generate == nil {
		return ""
	}
	if f := p.conf.Generate.featureOf(feature); f != nil && installFeatures()[feature] {
		return f.InstallDir
	}
	return ""
}

// emitInstallFiles writes the install-ready files of every enabled feature that sets
// install_dir: completions/<file> per shell, man/man<section>/<page>.<section> per listed man
// page, and <page>.md per listed markdown page, the name llms.txt links under base_url and the
// default markdown template links between pages. Each holds exactly what the generated accessor
// returns.
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
				name, ok := completionInstallNames()[n.name]
				if !ok {
					continue
				}
				rel = filepath.Join("completions", name(p.rootName))
			case o.desc.manPages:
				if !n.listed {
					continue
				}
				rel = filepath.Join("man", "man"+n.data.Section, n.file)
			case o.desc.name == markdownFeatureDesc.name:
				if !n.listed {
					continue
				}
				rel = n.data.PageName + ".md"
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
	if err := p.pruneInstalledMarkdown(); err != nil {
		return err
	}
	return p.pruneInstalledCompletion()
}

// pruneInstalledMarkdown removes the markdown pages under the markdown feature's install_dir
// that the previous generate listed in MarkdownPages and this run doesn't write: a removed or
// hidden command or topic, the old root's pages after a rename, and every page when the
// feature is off. The directory may hold the author's own pages too, so only listed names are
// candidates.
func (p *program) pruneInstalledMarkdown() error {
	dir := p.installDir(markdownFeatureDesc.name)
	if dir == "" || len(p.prior.markdownPages) == 0 {
		return nil
	}
	abs, err := underModule(p.module.root, "generate.features.markdown.install_dir", dir)
	if err != nil {
		return err
	}
	current := map[string]bool{}
	for _, o := range p.featureOutputs {
		if o.desc.name != markdownFeatureDesc.name {
			continue
		}
		for _, n := range o.nodes {
			if n.listed {
				current[n.data.PageName] = true
			}
		}
	}
	for _, name := range p.prior.markdownPages {
		if current[name] {
			continue
		}
		path := filepath.Join(abs, name+".md")
		exists, err := p.plan.exists(path)
		if err == nil && exists {
			err = p.plan.remove(path)
		}
		if err != nil {
			return fmt.Errorf("remove a stale installed markdown page: %w", err)
		}
		if exists && len(current) > 0 {
			p.pruned = append(p.pruned, path)
		}
	}
	return nil
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
			if err := p.plan.remove(filepath.Join(abs, "completions", completionInstallNames()[shell](root))); err != nil {
				return fmt.Errorf("remove a stale installed completion script: %w", err)
			}
		}
	}
	return nil
}
