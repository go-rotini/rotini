package codegen

import (
	"fmt"
	"go/token"
	"path"
	"strings"
)

// lintPackageNames rejects a target the go tool refuses: a `file` in a directory named after a
// Windows device (CON, AUX, NUL, LPT1 and so on, which the go tool disallows in import paths on
// every system), a file so named (Windows can't create it, and the go tool refuses it in a
// module zip), a `package` name that is a Go keyword, and `package: main` on a target other
// packages import.
func lintPackageNames(conf *Conf) []error {
	if conf.Generate == nil {
		return nil
	}
	var problems []error
	for i, p := range conf.Generate.Packages {
		file := path.Clean(strings.ReplaceAll(p.File, `\`, "/"))
		if el := windowsDeviceElement(path.Dir(file)); el != "" {
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/file", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("file %s has the path element %q, a Windows device name the go tool refuses in an import path on every system; choose another directory", quotePath(p.File), el)})
		} else if el := windowsDeviceElement(path.Base(file)); el != "" {
			device, _, _ := strings.Cut(el, ".")
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/file", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("file %s is named after the Windows device %s: Windows can't create it, and the go tool refuses it in a module zip, so the module can't be published; choose another file name", quotePath(p.File), strings.ToUpper(device))})
		}
		switch {
		case token.IsKeyword(p.Package):
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/package", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("package %q is a Go keyword, which can't name a package; choose another name (e.g. %q)", p.Package, p.Package+"cli")})
		case p.Package == "main" && p.Type != typeMain:
			problems = append(problems, &problem{kind: "conf", ptr: packagePointer(i) + "/package", loc: "generate.packages." + p.Type,
				msg: fmt.Sprintf("package \"main\" is a program, which the %s package can't be, since other packages import it; choose another name (e.g. \"maincli\")", p.Type)})
		}
	}
	return problems
}

// lintPackagePaths rejects a root command named after a Windows device (see lintPackageNames)
// when the conf leaves the cmd package at its default, internal/cmd/<root>.
func lintPackagePaths(spec *Spec, conf *Conf) []error {
	if conf != nil && conf.Generate != nil {
		if p := conf.Generate.cmdPkg(); p != nil && p.File != "" {
			return nil
		}
	}
	name := spec.Command.Name
	if windowsDeviceElement(name) == "" {
		return nil
	}
	return []error{&problem{kind: "spec", ptr: rootPointer + "/name", loc: rootLabel(spec),
		msg: fmt.Sprintf("the name %q is a Windows device name, and the cmd package defaults to internal/cmd/%s, which the go tool refuses in an import path on every system; set the cmd target's `file` in the conf to another directory, or rename the command", name, name)}}
}

// lintHandlerFileTargets rejects a command whose handler file would be the cmd target's
// generated file, or the models file in the cmd package's directory: generate would
// write one over the other, or skip the handler as already written, and the package wouldn't
// build. Names compare case-folded, as lintHandlerFilenames does.
func lintHandlerFileTargets(spec *Spec, conf *Conf) []error {
	if spec == nil || spec.Command.Name == "" {
		return nil
	}
	rootName := spec.Command.Name
	var g *GenerateConfig
	if conf != nil {
		g = conf.Generate
	}
	cmdFile := cmdTargetFile(g, rootName)
	cmdDir := path.Dir(cmdFile)
	targets := map[string]string{strings.ToLower(path.Base(cmdFile)): typeCmd}
	if p := g.packageOf(typeModels); p != nil && p.File != "" {
		file := slashClean(p.File)
		if path.Dir(file) == cmdDir {
			if _, taken := targets[strings.ToLower(path.Base(file))]; !taken {
				targets[strings.ToLower(path.Base(file))] = typeModels
			}
		}
	}

	var problems []error
	var walk func(c *Command, path, display, ptr string)
	walk = func(c *Command, path, display, ptr string) {
		if c.Handler == nil {
			fn := commandStubFilename(rootName, path, c.Filename)
			if typ, clash := targets[strings.ToLower(fn)]; clash {
				at := ptr
				if c.Filename != "" {
					at += "/filename"
				}
				problems = append(problems, &problem{kind: "spec", ptr: at, loc: "command " + display, msg: fmt.Sprintf(
					"generates handler file %q, the file the conf's generate.packages.%s target writes; set this command's `filename`, or choose another `file` for that target", fn, typ)})
			}
		}
		for i := range c.Commands {
			child := &c.Commands[i]
			if child.Ref != "" {
				continue // composed: its handler lives in the child's package
			}
			childPath := child.Name
			if path != "" {
				childPath = path + "_" + child.Name
			}
			walk(child, childPath, display+"/"+child.Name, fmt.Sprintf("%s/commands/%d", ptr, i))
		}
	}
	walk(&spec.Command, "", rootName, rootPointer)
	return problems
}

// lintMainDir rejects a main `file` in the directory of the cmd package, or of a models package
// of its own: main.go is package main, and a directory holds one package, so neither would
// build.
func lintMainDir(spec *Spec, conf *Conf) []error {
	if spec == nil || spec.Command.Name == "" || conf == nil || conf.Generate == nil {
		return nil
	}
	g := conf.Generate
	mainAt := -1
	for i, p := range g.Packages {
		if p.Type == typeMain && p.File != "" {
			mainAt = i
		}
	}
	if mainAt < 0 {
		return nil
	}
	mainFile := g.Packages[mainAt].File
	mainDir := path.Dir(slashClean(mainFile))
	other := ""
	if path.Dir(cmdTargetFile(g, spec.Command.Name)) == mainDir {
		other = typeCmd
	} else if m := g.packageOf(typeModels); m != nil && m.File != "" && path.Dir(slashClean(m.File)) == mainDir {
		other = typeModels
	}
	if other == "" {
		return nil
	}
	return []error{&problem{kind: "conf", ptr: packagePointer(mainAt) + "/file", loc: "generate.packages.main", msg: fmt.Sprintf(
		"file %s is in %s, the %s package's directory, but the entrypoint is package main and a directory holds one package, so it can't build; put it in a directory of its own, such as \"cmd/%s/main.go\"", quotePath(mainFile), quotePath(mainDir), other, spec.Command.Name)}}
}

// cmdTargetFile returns the slash-separated, cleaned file of the cmd target: the conf's, or
// the default internal/cmd/<root>/zz_rotini.go.
func cmdTargetFile(g *GenerateConfig, rootName string) string {
	if p := g.cmdPkg(); p != nil && p.File != "" {
		return slashClean(p.File)
	}
	return "internal/cmd/" + rootName + "/zz_rotini.go"
}

// slashClean returns a conf file path with forward slashes, cleaned.
func slashClean(p string) string { return path.Clean(strings.ReplaceAll(p, `\`, "/")) }

// windowsDeviceElement returns the first element of the slash-separated path p that Windows
// reserves as a device name (the part before any dot, in any case) and the go tool refuses,
// or "". COM0 and LPT0 are reserved by Windows too, but the go tool accepts them.
func windowsDeviceElement(p string) string {
	for el := range strings.SplitSeq(path.Clean(strings.ReplaceAll(p, `\`, "/")), "/") {
		stem, _, _ := strings.Cut(el, ".")
		switch s := strings.ToUpper(stem); {
		case s == "CON" || s == "PRN" || s == "AUX" || s == "NUL":
			return el
		case len(s) == 4 && (strings.HasPrefix(s, "COM") || strings.HasPrefix(s, "LPT")) && s[3] >= '1' && s[3] <= '9':
			return el
		}
	}
	return ""
}
