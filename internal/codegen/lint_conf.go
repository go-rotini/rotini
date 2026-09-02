package codegen

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// This file holds the conf lint STAGE: the lintConf method plus the rotini-specific
// conf rules (the checks the JSON Schema cannot express). Each rule is a pure
// func(*Conf) []error, registered in confLints.

// lintConf is the lint stage for the conf: it runs every conf rule over the reconciled
// conf, returning every problem. It assumes the conf is schema-valid (the Processor runs
// it only after validateConf passes).
func (p *Processor) lintConf(rc *reconciledConf) []error {
	problems := make([]error, 0, len(confLints))
	for _, rule := range confLints {
		problems = append(problems, rule(rc.conf)...)
	}
	return problems
}

// confLints is the ordered set of conf rules run after the conf is schema-valid,
// mirroring specLints. Like the spec rules, they reject configuration that would
// be silently ignored or that generate would reject later — validate is the gate.
var confLints = []func(*Conf) []error{
	lintPackageTypes,
	lintFeatureTypes,
	lintPackageColocation,
	lintEntrypoint,
	lintFeatureDirs,
	lintFeatureKnobs,
}

// lintPackageTypes rejects a generate.packages array naming the same `type` twice, which would
// make a target's destination ambiguous. The JSON Schema cannot express it: uniqueItems
// compares whole items, not one field.
func lintPackageTypes(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	seen := map[string]bool{}
	for _, p := range conf.Generate.Packages {
		if seen[p.Type] {
			problems = append(problems, &problem{
				kind: "conf", loc: "generate.packages",
				msg: fmt.Sprintf("type %q is declared more than once — each package type may appear at most once", p.Type),
			})
		}
		seen[p.Type] = true
	}
	return problems
}

// lintFeatureTypes rejects a generate.features array that names the same `type`
// twice, for the same reason as lintPackageTypes (the schema enums the type but
// cannot bound it to one entry).
func lintFeatureTypes(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	seen := map[string]bool{}
	for _, f := range conf.Generate.Features {
		if seen[f.Type] {
			problems = append(problems, &problem{
				kind: "conf", loc: "generate.features",
				msg: fmt.Sprintf("type %q is declared more than once — each feature type may appear at most once", f.Type),
			})
		}
		seen[f.Type] = true
	}
	return problems
}

// lintPackageColocation rejects two package targets that write the same file but declare
// different Go packages: the second write would clobber the first with a conflicting `package`
// clause. Targets that omit `package` derive it from the directory and never conflict.
func lintPackageColocation(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	byFile := map[string]string{} // file → first explicit package seen
	for _, p := range conf.Generate.Packages {
		if p.File == "" || p.Package == "" {
			continue
		}
		if prev, ok := byFile[p.File]; ok && prev != p.Package {
			problems = append(problems, &problem{
				kind: "conf", loc: "generate.packages",
				msg: fmt.Sprintf("file %q is targeted by package %q and %q — targets sharing a file must declare the same package", p.File, prev, p.Package),
			})
			continue
		}
		byFile[p.File] = p.Package
	}
	return problems
}

// lintEntrypoint rejects a main block whose 'keep' would be silently ignored:
// keep only takes effect once the entrypoint is actually written and its
// directory pruned, and that happens only when 'file' is set. (The entrypoint's
// Go package is always 'main'; there is no package key to reconcile.)
func lintEntrypoint(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.mainPkg() == nil {
		return nil
	}
	ep := conf.Generate.mainPkg()
	if ep.File == "" && len(ep.Keep) > 0 {
		return []error{&problem{
			kind: "conf",
			loc:  "generate.packages.main.keep",
			msg:  "has no effect without generate.packages.main.file — the entrypoint is only written, and its directory pruned, when file is set",
		}}
	}
	return nil
}

// lintFeatureDirs rejects an enabled embedding feature whose explicit embed_dir cannot resolve
// under an explicitly-set cmd package, which //go:embed could never reach. Only embed mode is
// checked: an inline feature writes no embedded file, and template_dir is never embedded. When
// either side is unset the defaults guarantee nesting.
func lintFeatureDirs(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	fw := conf.Generate.cmdPkg()
	if len(conf.Generate.Features) == 0 || fw == nil || fw.File == "" {
		return nil
	}
	cmdDir := path.Dir(filepath.ToSlash(fw.File))
	var problems []error
	check := func(name string, f *Feature) {
		if f == nil || !f.Enabled || !f.Embed || f.EmbedDir == "" {
			return
		}
		dir := path.Clean(filepath.ToSlash(f.EmbedDir))
		if dir != cmdDir && !strings.HasPrefix(dir, cmdDir+"/") {
			problems = append(problems, &problem{
				kind: "conf",
				loc:  "generate.features." + name + ".embed_dir",
				msg:  fmt.Sprintf("%q must resolve under the cmd package %q so //go:embed can reach it", f.EmbedDir, cmdDir),
			})
		}
	}
	check("help", conf.Generate.featureOf("help"))
	check("man", conf.Generate.featureOf("man"))
	check("markdown", conf.Generate.featureOf("markdown"))
	check("completion", conf.Generate.featureOf("completion"))
	return problems
}

// lintFeatureKnobs warns when an enabled feature sets a directory knob its mode ignores: an
// `embed_dir` without embed mode, a `template_dir` without seeding a template, or either on
// completion, which has no editable template. It warns rather than fails because the override
// is inert rather than broken. Disabled features are left alone as staged config.
func lintFeatureKnobs(conf *Conf) []error {
	if conf.Generate == nil || len(conf.Generate.Features) == 0 {
		return nil
	}
	var problems []error
	warn := func(name, key, msg string) {
		problems = append(problems, &problem{
			kind: "conf", loc: "generate.features." + name + "." + key,
			sev: severityWarning, msg: msg,
		})
	}
	for _, cf := range featureConfigs(conf) {
		f := cf.cfg
		if f == nil || !f.Enabled {
			continue
		}
		name := cf.desc.name
		if f.EmbedDir != "" && !f.Embed {
			warn(name, "embed_dir", "is set but embed is false — embed_dir is used only in embed mode (//go:embed); inline content writes no file, so it is ignored")
		}
		if cf.desc.tmplFile == "" { // no editable template (completion)
			if f.Template {
				warn(name, "template", name+" has no editable template — 'template' has no effect here")
			}
			if f.TemplateDir != "" {
				warn(name, "template_dir", name+" has no editable template — 'template_dir' has no effect here")
			}
			continue
		}
		if f.TemplateDir != "" && !f.Template {
			warn(name, "template_dir", "is set but template is false — template_dir is used only when the editable template is seeded (template: true); it is otherwise ignored")
		}
	}
	return problems
}
