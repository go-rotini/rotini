package internal

import "context"

// ReadConf reads and decodes the rotini conf file at path. The
// serialization (YAML, JSON, or JSONC) is selected from the file
// extension. ReadConf does not validate the document against the conf
// schema; use [Validate] for that.
func ReadConf(path string) (*Conf, error) {
	return readFile[Conf](path)
}

// WriteConf encodes c and writes it to path, selecting the serialization
// from the file extension.
func WriteConf(path string, c *Conf) error {
	return writeFile(path, c)
}

// WatchConf watches the conf file at path, invoking onChange with a freshly
// decoded *Conf after each change until ctx is canceled. Decode errors are
// passed to onChange so callers can surface them.
func WatchConf(ctx context.Context, path string, onChange func(*Conf, error)) error {
	return watchFile(ctx, path, onChange)
}
