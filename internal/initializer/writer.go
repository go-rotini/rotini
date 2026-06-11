package initializer

import (
	_ "embed"
)

type writer struct{}

func writeFile(path string, bytes []byte) error {
	// write a single file
	return nil
}

func writeFiles(renderedFiles renderedFiles) error {
	// write all files concurrently
	return nil
}

func removeFile() error {
	// cleanup a file on bad write/error
	return nil
}

func removeFiles() error {
	// cleanup all files on err, dispatching to removeFile for each
	return nil
}
