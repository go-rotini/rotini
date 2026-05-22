package internal

import "context"

// ReadSpec reads and decodes the rotini spec file at path. The
// serialization (YAML, JSON, or JSONC) is selected from the file
// extension. ReadSpec does not validate the document against the spec
// schema; use [Validate] for that.
func ReadSpec(path string) (*Spec, error) {
	return readFile[Spec](path)
}

// WriteSpec encodes s and writes it to path, selecting the serialization
// from the file extension.
func WriteSpec(path string, s *Spec) error {
	return writeFile(path, s)
}

// WatchSpec watches the spec file at path, invoking onChange with a freshly
// decoded *Spec after each change until ctx is canceled. Decode errors are
// passed to onChange so callers can surface them.
func WatchSpec(ctx context.Context, path string, onChange func(*Spec, error)) error {
	return watchFile(ctx, path, onChange)
}
