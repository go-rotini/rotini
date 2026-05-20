package internal

import (
	"bytes"
	"fmt"
	"os"
	"text/template"
)

var specFilePaths = map[string]string{
	"json": ".rotini.spec.json",
	"yaml": ".rotini.spec.yaml",
}

var confFilePaths = map[string]string{
	"json": ".rotini.conf.json",
	"yaml": ".rotini.conf.yaml",
}

// Initialize creates new rotini spec and conf files and a main.go stub for a CLI project.
// name is the root command name, format is "json" or "yaml", force overwrites
// existing files, and version is the rotini version embedded in the schema URL.
func Initialize(name, format string, force bool, version string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("root command name is required")
	}

	if format == "" {
		format = "yaml"
	}

	specPath, ok := specFilePaths[format]
	if !ok {
		return false, fmt.Errorf("unsupported format %q: must be \"json\" or \"yaml\"", format)
	}
	confPath := confFilePaths[format]

	moduleName, err := getModuleName()
	if err != nil {
		return false, fmt.Errorf("failed to determine module name: %w", err)
	}

	tmplData := map[string]any{
		"RotiniVersion":   version,
		"RootCommandName": name,
	}

	specTmpl := rotiniSpecYamlTmpl
	if format == "json" {
		specTmpl = rotiniSpecJsonTmpl
	}
	specContent, err := renderInitTemplate("spec", specTmpl, tmplData)
	if err != nil {
		return false, fmt.Errorf("failed to render spec template: %w", err)
	}
	if err := initWriteFile(specPath, specContent, force); err != nil {
		return false, err
	}

	confTmpl := rotiniConfYamlTmpl
	if format == "json" {
		confTmpl = rotiniConfJsonTmpl
	}
	confContent, err := renderInitTemplate("conf", confTmpl, tmplData)
	if err != nil {
		return false, fmt.Errorf("failed to render conf template: %w", err)
	}
	if err := initWriteFile(confPath, confContent, force); err != nil {
		return false, err
	}

	mainContent, err := renderTemplate("main.go", mainTmpl, map[string]any{
		"RotiniSpecFilePath":       specPath,
		"CommandsPackageImport":    moduleName + "/internal/cli/cmd",
		"CommandsPackageQualifier": "cmd",
	})
	if err != nil {
		return false, fmt.Errorf("failed to render main.go template: %w", err)
	}
	if err := initWriteFile("main.go", mainContent, force); err != nil {
		return false, err
	}

	return true, nil
}

// renderInitTemplate renders a plain-text (non-Go) template, used for JSON/YAML spec files.
func renderInitTemplate(name, tmplStr string, data map[string]any) ([]byte, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template %q: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("failed to execute template %q: %w", name, err)
	}
	return buf.Bytes(), nil
}

// initWriteFile writes content to path, returning an error if the file already
// exists and force is false.
func initWriteFile(path string, content []byte, force bool) error {
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("file already exists: %s (use --force to overwrite)", path)
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", path, err)
	}
	return nil
}
