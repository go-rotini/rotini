package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-rotini/fs"
	"github.com/go-rotini/jsonc"
	"github.com/go-rotini/yaml"
)

// fileFormat identifies the on-disk serialization of a rotini spec or
// conf file.
type fileFormat int

const (
	formatUnknown fileFormat = iota
	formatYAML
	formatJSON
	formatJSONC
)

// ErrUnsupportedFormat is returned for a spec or conf path whose extension
// is not one of the supported serializations (.yaml, .yml, .json, .jsonc).
var ErrUnsupportedFormat = errors.New("unsupported file format")

// detectFormat maps a file path's extension to its serialization format,
// returning formatUnknown for unrecognized extensions.
func detectFormat(path string) fileFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml":
		return formatYAML
	case ".json":
		return formatJSON
	case ".jsonc":
		return formatJSONC
	default:
		return formatUnknown
	}
}

// readFile reads the file at path and decodes it into a value of type T,
// choosing the decoder from the file extension. YAML, JSON, and JSONC all
// honor the json struct tags carried by the generated Spec and Conf types.
func readFile[T any](path string) (*T, error) {
	format := detectFormat(path)
	if format == formatUnknown {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, path)
	}
	data, err := fs.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	out := new(T)
	switch format {
	case formatYAML:
		err = yaml.Unmarshal(data, out)
	case formatJSON:
		err = json.Unmarshal(data, out)
	case formatJSONC:
		err = jsonc.Unmarshal(data, out)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedFormat, path)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}

// writeFile encodes v in the format selected from path's extension and
// writes it atomically, creating parent directories as needed. JSONC files
// are written as standard JSON, which is a valid JSONC document.
func writeFile[T any](path string, v *T) error {
	var (
		data []byte
		err  error
	)
	switch detectFormat(path) {
	case formatYAML:
		data, err = yaml.Marshal(v)
	case formatJSON, formatJSONC:
		data, err = json.MarshalIndent(v, "", "  ")
		data = append(data, '\n')
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, path)
	}
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}

	if err := fs.WriteFile(path, data, fs.WithMkdirAll(true), fs.WithAtomic(true)); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// watchFile watches the file at path and invokes onChange with a freshly
// decoded *T after each content change, until ctx is canceled. Decode
// errors are delivered to onChange rather than stopping the watch, so a
// transient bad write does not end the stream.
func watchFile[T any](ctx context.Context, path string, onChange func(*T, error)) error {
	if detectFormat(path) == formatUnknown {
		return fmt.Errorf("%w: %s", ErrUnsupportedFormat, path)
	}
	w, err := fs.NewWatcher(path)
	if err != nil {
		return fmt.Errorf("watch %s: %w", path, err)
	}
	events, err := w.Subscribe(ctx)
	if err != nil {
		_ = w.Close()
		return fmt.Errorf("subscribe %s: %w", path, err)
	}

	go func() {
		defer w.Close()
		const relevant = fs.WatchWrite | fs.WatchCreate | fs.WatchRename
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-events:
				if !ok {
					return
				}
				if ev.Op&relevant != 0 {
					onChange(readFile[T](path))
				}
			}
		}
	}()
	return nil
}
