package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// Generate generates all program files based on the spec and conf files,
// optionally watching for changes. In watch mode it blocks until interrupted
// (Ctrl+C / SIGINT). Output paths are derived from the configuration file.
// If configFile is empty, the default conf file is discovered automatically.
func Generate(ctx context.Context, specFile, configFile string, watch bool) (bool, error) {
	specPath := resolveSpecPath(specFile)
	confPath := resolveConfPath(configFile)

	if err := runGenerate(specPath, confPath); err != nil {
		return false, err
	}
	if !watch {
		return true, nil
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	specInfo, err := os.Stat(specPath)
	if err != nil {
		return false, fmt.Errorf("watch: could not stat spec file: %w", err)
	}
	confInfo, err := os.Stat(confPath)
	if err != nil {
		return false, fmt.Errorf("watch: could not stat conf file: %w", err)
	}

	lastSpecMod := specInfo.ModTime()
	lastConfMod := confInfo.ModTime()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return true, nil
		case <-ticker.C:
			specInfo, err := os.Stat(specPath)
			if err != nil {
				return false, fmt.Errorf("watch: could not stat spec file: %w", err)
			}
			confInfo, err := os.Stat(confPath)
			if err != nil {
				return false, fmt.Errorf("watch: could not stat conf file: %w", err)
			}
			if specInfo.ModTime().Equal(lastSpecMod) && confInfo.ModTime().Equal(lastConfMod) {
				continue
			}
			lastSpecMod = specInfo.ModTime()
			lastConfMod = confInfo.ModTime()
			if err := runGenerate(specPath, confPath); err != nil {
				return false, err
			}
		}
	}
}

// runGenerate performs a single generation pass for the given spec and conf file paths.
func runGenerate(specPath, confPath string) error {
	s, err := newSpec(specPath, confPath)
	if err != nil {
		return fmt.Errorf("failed to load spec: %w", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	cmdPkg := s.Configuration.Generate.Cmd.Package
	fwPkg := s.Configuration.Generate.Framework.Package

	handlersDir := filepath.Join(cwd, cmdPkg)
	handlersRollupFile := s.Configuration.Generate.Cmd.GenFile
	handlersPackageName := filepath.Base(cmdPkg)

	// Three output configurations are supported:
	//   Case 1 — separate packages: framework.package != cmd.package
	//             Framework types live in their own package; handler stubs import it.
	//   Case 2 — same package, two files: framework.package == cmd.package,
	//             framework.gen_file != cmd.gen_file
	//             Types and rollup are in the same package but different files;
	//             no cross-package import needed.
	//   Case 3 — same package, one file: framework.package == cmd.package,
	//             framework.gen_file == cmd.gen_file
	//             Everything (types + Handlers rollup) goes into a single file;
	//             no separate rollup file is written.
	samePackage := filepath.Clean(fwPkg) == filepath.Clean(cmdPkg)
	genFile := s.Configuration.Generate.Framework.GenFile
	if genFile == "" {
		genFile = "rotini.gen.go"
	}
	sameFile := samePackage && (genFile == handlersRollupFile)
	merged := samePackage && !sameFile // Cases 2 and 3 both suppress the framework import

	converted := convertDefinition(s)

	metadataDefs := make([]metadataDef, len(s.Definition.Metadata))
	for i, m := range s.Definition.Metadata {
		metadataDefs[i] = metadataDef{Var: m.Var, Default: m.Default}
	}

	moduleName, err := getModuleName()
	if err != nil {
		return fmt.Errorf("failed to get module name: %w", err)
	}

	var fwDir string
	var fwPackageName string
	var fwImportPath string
	if samePackage {
		fwDir = handlersDir
		fwPackageName = handlersPackageName
		fwImportPath = ""
	} else {
		fwDir = filepath.Join(cwd, fwPkg)
		fwPackageName = filepath.Base(fwPkg)
		fwImportPath = calculateImportPath(moduleName, fwPkg)
	}

	if _, err := generateProgramFile(codegenOptions{
		PackageName:        fwPackageName,
		OutputDir:          fwDir,
		OutputFile:         genFile,
		Commands:           converted.Commands,
		RootFlags:          converted.RootFlags,
		RootStdin:          converted.RootStdin,
		ConfigurationFiles: converted.ConfigurationFiles,
		RemoteCommands:     converted.RemoteCommands,
		Bin:                binConfig{Name: toPascalCase(s.Definition.Name), DisplayName: s.Definition.Name},
		Imports:            s.Configuration.Generate.Framework.AdditionalImports,
		Timeout:            converted.Timeout,
		Metadata:           metadataDefs,
		AllEvents:          converted.AllEvents,
		AllCustomTypes:     converted.AllCustomTypes,
		Merged:             merged,
		SameFile:           sameFile,
	}); err != nil {
		return fmt.Errorf("failed to generate program file: %w", err)
	}

	if _, err := syncImplFiles(
		handlersPackageName,
		handlersDir,
		handlersRollupFile,
		s.Definition.Name,
		converted.Commands,
		converted.RootFlags,
		converted.ConfigurationFiles,
		converted.AllEvents,
		s.Configuration.Generate.Framework.AdditionalImports,
		s.Configuration.Generate.Cmd.Prune,
		merged || sameFile,
		fwImportPath,
		sameFile,
	); err != nil {
		return fmt.Errorf("failed to sync handler files: %w", err)
	}

	return nil
}

// resolveSpecPath returns the spec file path to use.
// YAML is the primary format (.rotini.spec.yaml). When path is empty or is a
// known default name that doesn't exist, discovery tries the new names first
// then falls back to legacy names (.rotini.yaml, .rotini.json) for backward
// compatibility.
func resolveSpecPath(path string) string {
	isDefault := path == "" || path == fileNameSpec || path == fileNameSpecJSON ||
		path == ".rotini.yaml" || path == ".rotini.json"
	if isDefault {
		for _, candidate := range []string{fileNameSpec, fileNameSpecJSON, ".rotini.yaml", ".rotini.json"} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		return fileNameSpec
	}
	return path
}

// resolveConfPath returns the conf file path to use.
// YAML is the primary format (.rotini.conf.yaml). When path is empty or is a
// known default name that doesn't exist, discovery tries YAML first then JSON.
func resolveConfPath(path string) string {
	isDefault := path == "" || path == fileNameConf || path == fileNameConfJSON
	if isDefault {
		if _, err := os.Stat(fileNameConf); err == nil {
			return fileNameConf
		}
		if _, err := os.Stat(fileNameConfJSON); err == nil {
			return fileNameConfJSON
		}
		return fileNameConf
	}
	return path
}
