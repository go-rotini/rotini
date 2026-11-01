package codegen

import (
	"cmp"
	"fmt"
	"path"
	"strings"
)

// generatorPointer returns the JSON pointer of the i'th generate.generators entry.
func generatorPointer(i int) string { return fmt.Sprintf("/generate/generators/%d", i) }

// lintGenerators checks generate.generators: each name is unique; each dir stays in the module,
// isn't its root, and neither contains nor sits inside another generator's dir or a path rotini
// writes itself; and each keep path is one the generator could return. It reads the spec for
// the cmd package's default directory.
func lintGenerators(spec *Spec, conf *Conf) []error {
	if conf == nil || conf.Generate == nil || len(conf.Generate.Generators) == 0 {
		return nil
	}
	rootName := ""
	if spec != nil {
		rootName = spec.Command.Name
	}
	outputs := rotiniOutputPaths(conf.Generate, rootName)
	var problems []error
	add := func(i int, field, msg string) {
		problems = append(problems, &problem{kind: "conf", ptr: generatorPointer(i) + field, loc: "generate.generators." + conf.Generate.Generators[i].Name + strings.ReplaceAll(field, "/", "."), msg: msg})
	}
	names := map[string]bool{}
	dirs := make([]string, len(conf.Generate.Generators)) // each valid dir, cleaned
	for i, gen := range conf.Generate.Generators {
		if names[gen.Name] {
			add(i, "/name", fmt.Sprintf("generator %q is declared more than once; each generator needs a name of its own", gen.Name))
		}
		names[gen.Name] = true
		for j, k := range gen.Keep {
			if why := generatorPathProblem(k); why != "" {
				add(i, fmt.Sprintf("/keep/%d", j), fmt.Sprintf("%s %s", quotePath(k), why))
			}
		}
		if why := escapesModule(gen.Dir); why != "" {
			add(i, "/dir", fmt.Sprintf("%s %s", quotePath(gen.Dir), why))
			continue
		}
		dir := slashClean(gen.Dir)
		if dir == "." {
			add(i, "/dir", fmt.Sprintf("%s is the module root; give the generator a directory of its own, such as \"docs/%s\"", quotePath(gen.Dir), gen.Name))
			continue
		}
		for j, other := range dirs[:i] {
			if other != "" && pathsOverlap(dir, other) {
				add(i, "/dir", fmt.Sprintf("%s overlaps generator %q's dir %s; each generator needs a directory of its own", quotePath(gen.Dir), conf.Generate.Generators[j].Name, quotePath(conf.Generate.Generators[j].Dir)))
				break
			}
		}
		dirs[i] = dir
		for _, o := range outputs {
			if pathsOverlap(dir, o.path) {
				add(i, "/dir", fmt.Sprintf("%s overlaps %s, which rotini writes (%s); give the generator a directory of its own", quotePath(gen.Dir), quotePath(o.path), o.key))
				break
			}
		}
	}
	return problems
}

// rotiniOutput is a path rotini writes, and the conf key that puts it there.
type rotiniOutput struct{ key, path string }

// rotiniOutputPaths lists the module-root-relative paths rotini's own outputs occupy, slash
// separated and cleaned: each package's directory, each feature's directories and files (a
// default one only when the feature is enabled), the schema files and directories and the
// contract file.
func rotiniOutputPaths(g *GenerateConfig, rootName string) []rotiniOutput {
	var out []rotiniOutput
	add := func(key, p string) {
		if p != "" && escapesModule(p) == "" {
			out = append(out, rotiniOutput{key: key, path: slashClean(p)})
		}
	}
	for _, p := range g.Packages {
		if p.File != "" {
			add("generate.packages."+p.Type, path.Dir(slashClean(p.File)))
		}
	}
	if rootName != "" {
		add("generate.packages.cmd", path.Dir(cmdTargetFile(g, rootName)))
	}
	for _, f := range g.Features {
		key := "generate.features." + f.Type
		add(key+".embed_dir", f.EmbedDir)
		add(key+".template_dir", f.TemplateDir)
		add(key+".install_dir", f.InstallDir)
		if pf, ok := programFeatureOf(f.Type); ok && (f.File != "" || f.Enabled) {
			add(key+".file", cmp.Or(f.File, pf.file))
		}
	}
	if s := g.Schemas; s != nil {
		if s.Conf != nil {
			add("generate.schemas.conf.file", s.Conf.File)
		}
		if s.Spec != nil {
			add("generate.schemas.spec.file", s.Spec.File)
		}
		if s.Output != nil {
			add("generate.schemas.output.dir", s.Output.Dir)
		}
		if s.Config != nil {
			add("generate.schemas.config.dir", s.Config.Dir)
		}
	}
	if c := g.Contract; c != nil {
		add("generate.contract.file", c.File)
	}
	return out
}

// pathsOverlap reports whether two cleaned, slash-separated, module-root-relative paths are the
// same or one is inside the other, ignoring case as macOS and Windows do.
func pathsOverlap(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	return a == b || b == "." || a == "." || strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/")
}
