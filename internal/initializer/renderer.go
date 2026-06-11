package initializer

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
	templateDefaultSpecText string
	//go:embed templates/.rotini.conf.yaml.tmpl
	templateDefaultConfText string

	errUnsupportedFormat = errors.New("unsupported file format")
)

type renderedFiles struct {
	SpecFileBytes []byte
	ConfFileBytes []byte
}

type renderer struct {
	version    string
	pkg        string
	fileFormat FileFormat
}

type templateDefaultSpecData struct {
	Version string
	Package string
}

type templateDefaultConfData struct {
	Version string
	Package string
}

func format(yamlBytes []byte, fileFormat FileFormat) ([]byte, error) {
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
		return nil, fmt.Errorf("%w: %s", errUnsupportedFormat, fileFormat)
	}
}

func renderTemplate[T any](name string, text string, data T, fileFormat FileFormat) ([]byte, error) {
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		return nil, fmt.Errorf("parse %s seed template: %w", name, err)
	}

	var buffer bytes.Buffer
	if err := tmpl.Execute(&buffer, data); err != nil {
		return nil, fmt.Errorf("render %s seed: %w", name, err)
	}

	return format(buffer.Bytes(), fileFormat)
}

func (r *renderer) renderDefaultSpecFile() ([]byte, error) {
	return renderTemplate(
		"spec",
		templateDefaultSpecText,
		templateDefaultSpecData{
			Version: r.version,
			Package: r.pkg,
		},
		r.fileFormat,
	)
}

func (r *renderer) renderDefaultConfFile() ([]byte, error) {
	return renderTemplate(
		"conf",
		templateDefaultConfText,
		templateDefaultConfData{
			Version: r.version,
			Package: r.pkg,
		},
		r.fileFormat,
	)
}

func renderFiles(version string, pkg string, fileFormat FileFormat) (*renderedFiles, error) {
	r := &renderer{
		version:    version,
		pkg:        pkg,
		fileFormat: fileFormat,
	}

	specFileBytes, err := r.renderDefaultSpecFile()
	if err != nil {
		return nil, fmt.Errorf("render spec: %w", err)
	}

	confFileBytes, err := r.renderDefaultConfFile()
	if err != nil {
		return nil, fmt.Errorf("render conf: %w", err)
	}

	return &renderedFiles{
		SpecFileBytes: specFileBytes,
		ConfFileBytes: confFileBytes,
	}, nil
}
