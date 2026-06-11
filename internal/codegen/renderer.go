package codegen

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"text/template"

	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/toml"
	"github.com/go-rotini/yaml"
)

type FileFormat string

const (
	FileFormatJSON  FileFormat = "json"
	FileFormatJSONC FileFormat = "jsonc"
	FileFormatTOML  FileFormat = "toml"
	FileFormatYAML  FileFormat = "yaml"
)

var (
	//go:embed templates/.rotini.spec.yaml.tmpl
	templateSpec string
	//go:embed templates/.rotini.conf.yaml.tmpl
	templateConf string
	//go:embed templates/main.go.tmpl
	templateMain string
	//go:embed templates/handler_stub.go.tmpl
	templateHandlerStub string
	//go:embed templates/handler_root.go.tmpl
	templateHandlerRoot string
	//go:embed templates/handler_version.go.tmpl
	templateHandlerVersion string
	//go:embed templates/handler_help.go.tmpl
	templateHandlerHelp string
	//go:embed templates/handlers.go.tmpl
	templateHandlers string
	//go:embed templates/rotini.go.tmpl
	templateRotini string
	//go:embed templates/help.txt.tmpl
	templateHelp string
	//go:embed templates/man.txt.tmpl
	templateMan string

	ErrUnsupportedFileFormat = errors.New("unsupported spec file format")
)

func convert(yamlBytes []byte, fileFormat FileFormat) ([]byte, error) {
	if fileFormat == FileFormatYAML {
		return yamlBytes, nil
	}

	jsonBytes, err := yaml.ToJSON(yamlBytes)
	if err != nil {
		return nil, fmt.Errorf("convert seed to json: %w", err)
	}

	switch fileFormat {
	case FileFormatJSON:
		var v any
		if err := json.Unmarshal(jsonBytes, &v); err != nil {
			return nil, fmt.Errorf("decode json: %w", err)
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode json: %w", err)
		}
		return append(out, '\n'), nil
	case FileFormatJSONC:
		var v any
		out, err := jsonc.MarshalIndent(v, "  ")
		if err != nil {
			return nil, fmt.Errorf("encode jsonc: %w", err)
		}
		return append(out, '\n'), nil
	case "toml":
		out, err := toml.FromJSON(jsonBytes)
		if err != nil {
			return nil, fmt.Errorf("convert to toml: %w", err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFileFormat, fileFormat)
	}
}

func renderTemplate[T any](name string, text string, data T) ([]byte, error) {
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s template: %w", name, err)
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("render %s template: %w", name, err)
	}

	return buffer.Bytes(), nil
}

type templateSpecData struct {
	Version string
	Package string
}

func renderSpecFile(version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	bytes, err := renderTemplate(
		"spec",
		templateSpec,
		templateSpecData{
			Version: version,
			Package: pkg,
		},
	)

	if err != nil {
		return nil, err
	}

	return convert(bytes, fileFormat)
}

type templateConfData struct {
	Version string
	Package string
}

func renderConfFile(version string, pkg string, fileFormat FileFormat) ([]byte, error) {
	bytes, err := renderTemplate(
		"conf",
		templateConf,
		templateConfData{
			Version: version,
			Package: pkg,
		},
	)

	if err != nil {
		return nil, err
	}

	return convert(bytes, fileFormat)
}

type templateMainData struct {
	Package      string
	PackageAlias string
}

func renderMainFile(pkg string, pkgAlias string) ([]byte, error) {
	bytes, err := renderTemplate(
		"main",
		templateMain,
		templateMainData{
			Package:      pkg,
			PackageAlias: pkgAlias,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerStubData struct {
	Package      string
	HandlersType string
}

func renderHandlerStubFile(pkg string, handlersType string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_stub",
		templateHandlerStub,
		templateHandlerStubData{
			Package:      pkg,
			HandlersType: handlersType,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerRootData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerRootFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_root",
		templateHandlerRoot,
		templateHandlerRootData{
			Package:         pkg,
			HandlersType:    handlersType,
			RootCommandName: rootCommandName,
			HelpVar:         helpVar,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerVersionData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerVersionFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_version",
		templateHandlerVersion,
		templateHandlerVersionData{
			Package:         pkg,
			HandlersType:    handlersType,
			RootCommandName: rootCommandName,
			HelpVar:         helpVar,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlerHelpData struct {
	Package         string
	HandlersType    string
	RootCommandName string
	HelpVar         string
}

func renderHandlerHelpFile(pkg string, handlersType string, rootCommandName string, helpVar string) ([]byte, error) {
	bytes, err := renderTemplate(
		"handler_help",
		templateHandlerHelp,
		templateHandlerHelpData{
			Package:         pkg,
			HandlersType:    handlersType,
			RootCommandName: rootCommandName,
			HelpVar:         helpVar,
		},
	)

	if err != nil {
		return nil, err
	}

	return bytes, nil
}

type templateHandlersData struct {
}

func renderHandlersFile() ([]byte, error) {
	return nil, nil
}

type templateRotiniData struct {
}

func renderRotiniFile() ([]byte, error) {
	return nil, nil
}

type templateHelpData struct {
}

func renderHelpFile() ([]byte, error) {
	return nil, nil
}

type templateManData struct {
}

func renderManFile() ([]byte, error) {
	return nil, nil
}
