// Package cfgedit changes one value in a config file's source text and leaves every other byte
// as it was: comments, key order, quoting, indentation, line endings and a byte-order mark.
//
// It indexes where each key and value sits in the source and splices the change into the
// original bytes; nothing is re-encoded. YAML, JSON and JSONC splice through their parsers'
// positions, dotenv replaces whole lines, and TOML splices through its node spans. After every
// edit the result is decoded again and compared with the original: only the edited key may
// differ, and it must hold the intended value. Any mismatch is an error and the edit is
// discarded, so an unusual layout fails closed instead of corrupting the file.
//
// The YAML span index ([IndexYAML]) is also the base of the companion CLI's canonical spec
// printer.
package cfgedit
