package codegen

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// The conf lint stage: rotini-specific rules the JSON Schema cannot express. Each rule
// is a pure func(*Conf) []error registered in confLints.

// lintConf runs every conf rule and positions each problem in source. It assumes the conf
// is schema-valid.
func (p *Processor) lintConf(rc *reconciledConf) []error {
	problems := make([]error, 0, len(confLints))
	for _, rule := range confLints {
		problems = append(problems, rule(rc.conf)...)
	}
	locateProblems(problems, rc.path, rc.locate)
	return problems
}

// packagePointer and featurePointer return the JSON pointer of the i'th generate.packages
// or generate.features entry. A negative index yields "", leaving the problem unplaced.
func packagePointer(i int) string {
	if i < 0 {
		return ""
	}
	return fmt.Sprintf("/generate/packages/%d", i)
}

func featurePointer(i int) string {
	if i < 0 {
		return ""
	}
	return fmt.Sprintf("/generate/features/%d", i)
}

// featureIndex returns the array position of the named feature, or -1 when absent.
func featureIndex(conf *Conf, name string) int {
	if conf.Generate == nil {
		return -1
	}
	for i, f := range conf.Generate.Features {
		if f.Type == name {
			return i
		}
	}
	return -1
}

// confLints is the ordered set of conf rules. They reject configuration that would be
// silently ignored or that generate would reject later.
var confLints = []func(*Conf) []error{
	lintPackageTypes,
	lintFeatureTypes,
	lintPackageColocation,
	lintEntrypoint,
	lintFeatureDirs,
	lintFeatureKnobs,
	lintFeatureSection,
	lintCompletionMessages,
	lintSchemaFiles,
	lintModelsKeep,
	lintInstallDir,
	lintSchemaDirs,
	lintDiffAccept,
	lintFeatureKeys,
	lintLlmsBaseURL,
	lintPackageNames,
}

// lintFeatureSection rejects `section`, the man page section number, on any feature but man,
// where it would be silently ignored.
func lintFeatureSection(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	for i, f := range conf.Generate.Features {
		if f.Section != 0 && f.Type != "man" {
			problems = append(problems, &problem{
				kind: "conf", ptr: featurePointer(i) + "/section", loc: "generate.features." + f.Type + ".section",
				msg: "`section` is the man page section and applies only to the man feature; move it to the man entry or remove it",
			})
		}
	}
	return problems
}

// lintPackageTypes rejects a generate.packages array naming the same `type` twice, which
// would make a target's destination ambiguous. JSON Schema uniqueItems compares whole items,
// so it cannot express this.
func lintPackageTypes(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	seen := map[string]bool{}
	for i, p := range conf.Generate.Packages {
		if seen[p.Type] {
			problems = append(problems, &problem{
				kind: "conf", ptr: packagePointer(i), loc: "generate.packages",
				msg: fmt.Sprintf("type %q is declared more than once; each package type may appear at most once", p.Type),
			})
		}
		seen[p.Type] = true
	}
	return problems
}

// lintFeatureTypes rejects a generate.features array naming the same `type` twice.
func lintFeatureTypes(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	seen := map[string]bool{}
	for i, f := range conf.Generate.Features {
		if seen[f.Type] {
			problems = append(problems, &problem{
				kind: "conf", ptr: featurePointer(i), loc: "generate.features",
				msg: fmt.Sprintf("type %q is declared more than once; each feature type may appear at most once", f.Type),
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
	for i, p := range conf.Generate.Packages {
		if p.File == "" || p.Package == "" {
			continue
		}
		if prev, ok := byFile[p.File]; ok && prev != p.Package {
			problems = append(problems, &problem{
				kind: "conf", ptr: packagePointer(i), loc: "generate.packages",
				msg: fmt.Sprintf("file %s is targeted by package %q and %q; targets sharing a file must declare the same package", quotePath(p.File), prev, p.Package),
			})
			continue
		}
		byFile[p.File] = p.Package
	}
	return problems
}

// lintEntrypoint rejects `keep` on a main package with no `file`: the entrypoint is written,
// and its directory pruned, only when `file` is set.
func lintEntrypoint(conf *Conf) []error {
	if conf.Generate == nil || conf.Generate.mainPkg() == nil {
		return nil
	}
	ep := conf.Generate.mainPkg()
	if ep.File == "" && len(ep.Keep) > 0 {
		mainAt := -1
		for i, p := range conf.Generate.Packages {
			if p.Type == "main" {
				mainAt = i
				break
			}
		}
		return []error{&problem{
			kind: "conf",
			ptr:  packagePointer(mainAt),
			loc:  "generate.packages.main.keep",
			msg:  "has no effect without `generate.packages.main.file`; the entrypoint is only written, and its directory pruned, when `file` is set",
		}}
	}
	return nil
}

// lintFeatureDirs rejects an enabled embed-mode feature whose explicit embed_dir does not
// resolve under the cmd package, where //go:embed could not reach it.
//
// With no cmd package file declared, the default is internal/cmd/<root name>. The root name
// lives in the spec, so this rule only requires internal/cmd/<some name>; generate checks the
// exact directory.
func lintFeatureDirs(conf *Conf) []error {
	if conf.Generate == nil || len(conf.Generate.Features) == 0 {
		return nil
	}
	cmdDir, under := "internal/cmd/<root name>", func(dir string) bool {
		rest, ok := strings.CutPrefix(dir, "internal/cmd/")
		return ok && rest != "" && !strings.HasPrefix(rest, "..")
	}
	if fw := conf.Generate.cmdPkg(); fw != nil && fw.File != "" {
		cmdDir = path.Dir(filepath.ToSlash(fw.File))
		under = func(dir string) bool { return dir == cmdDir || strings.HasPrefix(dir, cmdDir+"/") }
	}
	var problems []error
	check := func(name string, f *Feature) {
		if f == nil || !f.Enabled || !f.Embed || f.EmbedDir == "" {
			return
		}
		if !under(path.Clean(filepath.ToSlash(f.EmbedDir))) {
			problems = append(problems, &problem{
				kind: "conf",
				ptr:  featurePointer(featureIndex(conf, name)) + "/embed_dir",
				loc:  "generate.features." + name + ".embed_dir",
				msg:  fmt.Sprintf("%s must resolve under the cmd package %s so //go:embed can reach it", quotePath(f.EmbedDir), quotePath(cmdDir)),
			})
		}
	}
	for _, cf := range featureConfigs(conf) {
		check(cf.desc.name, cf.cfg)
	}
	return problems
}

// lintFeatureKnobs warns when an enabled feature sets a knob its mode ignores: `embed_dir`
// without `embed`, `template_dir` without `template`, or `template`/`template_dir` on a
// feature with no editable template (completion). Disabled features are skipped.
func lintFeatureKnobs(conf *Conf) []error {
	if conf.Generate == nil || len(conf.Generate.Features) == 0 {
		return nil
	}
	var problems []error
	warn := func(name, key, msg string) {
		problems = append(problems, &problem{
			kind: "conf", ptr: featurePointer(featureIndex(conf, name)) + "/" + key,
			loc: "generate.features." + name + "." + key,
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
			warn(name, "embed_dir", "is set but `embed` is false; `embed_dir` is used only in embed mode (//go:embed); inline content writes no file, so it is ignored")
		}
		if cf.desc.tmplFile == "" { // no editable template (completion)
			if f.Template {
				warn(name, "template", name+" has no editable template; `template` has no effect here")
			}
			if f.TemplateDir != "" {
				warn(name, "template_dir", name+" has no editable template; `template_dir` has no effect here")
			}
			continue
		}
		if f.TemplateDir != "" && !f.Template {
			warn(name, "template_dir", "is set but `template` is false; `template_dir` is used only when the editable template is seeded (`template: true`); it is otherwise ignored")
		}
	}
	return problems
}

// lintSchemaFiles rejects a file path rotini writes (a generate.schemas, generate.contract or
// generate.packages file) that is absolute or escapes the module root, and a spec and conf
// schema written to one file, where the second would overwrite the first.
func lintSchemaFiles(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	check := func(file, ptr, loc string) {
		if file == "" {
			return
		}
		var why string
		switch clean := path.Clean(filepath.ToSlash(file)); {
		case path.IsAbs(clean) || filepath.IsAbs(file):
			why = "must be module-root-relative, not absolute"
		case clean == ".." || strings.HasPrefix(clean, "../"):
			why = "must resolve under the module root"
		default:
			return
		}
		problems = append(problems, &problem{kind: "conf", ptr: ptr, loc: loc, msg: fmt.Sprintf("%s %s", quotePath(file), why)})
	}
	if s := conf.Generate.Schemas; s != nil {
		for _, sc := range []struct {
			label string
			c     *SchemaConfig
		}{{"spec", s.Spec}, {"conf", s.Conf}} {
			if sc.c != nil {
				check(sc.c.File, "/generate/schemas/"+sc.label+"/file", "generate.schemas."+sc.label+".file")
			}
		}
		if s.Spec != nil && s.Conf != nil && s.Spec.File != "" && path.Clean(filepath.ToSlash(s.Spec.File)) == path.Clean(filepath.ToSlash(s.Conf.File)) {
			problems = append(problems, &problem{kind: "conf", ptr: "/generate/schemas/conf/file", loc: "generate.schemas.conf.file",
				msg: fmt.Sprintf("%s is also generate.schemas.spec.file; each schema needs its own file, or the second overwrites the first", quotePath(s.Conf.File))})
		}
	}
	if c := conf.Generate.Contract; c != nil {
		check(c.File, "/generate/contract/file", "generate.contract.file")
	}
	for i, p := range conf.Generate.Packages {
		check(p.File, packagePointer(i)+"/file", "generate.packages."+p.Type+".file")
	}
	return problems
}

// lintModelsKeep rejects `keep` on the models package, which is never pruned.
func lintModelsKeep(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	for i, p := range conf.Generate.Packages {
		if p.Type == typeModels && len(p.Keep) > 0 {
			problems = append(problems, &problem{
				kind: "conf", ptr: packagePointer(i) + "/keep", loc: "generate.packages.models.keep",
				msg: "has no effect; nothing is pruned from the models package, so there is nothing to keep; remove it",
			})
		}
	}
	return problems
}

// lintCompletionMessages keeps `messages`, `messages_env` and `descriptions_env` to the
// completion feature, where they switch completion messages and descriptions, requires
// `messages` for `messages_env`, which would otherwise switch nothing, and warns when the two
// variables are one.
func lintCompletionMessages(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	add := func(i int, f Feature, key, msg string) {
		problems = append(problems, &problem{
			kind: "conf", ptr: featurePointer(i) + "/" + key, loc: "generate.features." + f.Type + "." + key, msg: msg,
		})
	}
	for i, f := range conf.Generate.Features {
		if f.Type != "completion" {
			if f.Messages != "" {
				add(i, f, "messages", "`messages` turns on completion messages and applies only to the completion feature; move it to the completion entry or remove it")
			}
			if f.MessagesEnv != "" {
				add(i, f, "messages_env", "`messages_env` switches completion messages and applies only to the completion feature; move it to the completion entry or remove it")
			}
			if f.DescriptionsEnv != "" {
				add(i, f, "descriptions_env", "`descriptions_env` switches completion descriptions and applies only to the completion feature; move it to the completion entry or remove it")
			}
			continue
		}
		if f.MessagesEnv != "" && f.Messages == "" {
			add(i, f, "messages_env", "is set but `messages` is not, so there are no completion messages for it to switch; set `messages: declared` or `messages: all`, or remove it")
		}
		if f.DescriptionsEnv != "" && f.DescriptionsEnv == f.MessagesEnv {
			problems = append(problems, &problem{
				kind: "conf", ptr: featurePointer(i) + "/descriptions_env", loc: "generate.features.completion.descriptions_env", sev: severityWarning,
				msg: "names the same variable as `messages_env`, so one setting hides both messages and descriptions; give each its own variable",
			})
		}
	}
	return problems
}
