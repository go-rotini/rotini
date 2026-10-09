package rotini

import (
	"slices"
	"strings"
)

// acquiredLines splits text rotini read from a file or stdin into its items: one per line, a
// trailing "\r" removed, skipping empty and blank lines, and with comments also lines whose
// first non-blank character is '#'. Other lines are kept exactly, spaces included. With nul
// the items are separated by NUL bytes instead and kept byte for byte, only empty ones
// skipped. One leading byte-order mark is removed first.
func acquiredLines(text string, nul, comments bool) []string {
	text = strings.TrimPrefix(text, utf8BOM)
	var items []string
	if nul {
		for item := range strings.SplitSeq(text, "\x00") {
			if item != "" {
				items = append(items, item)
			}
		}
		return items
	}
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || (comments && strings.HasPrefix(trimmed, "#")) {
			continue
		}
		items = append(items, line)
	}
	return items
}

// acquiresValue reports whether an argv value of fd is replaced by content rotini reads: an
// `@file` with `from: [file]` (not the doubled `@@` escape), or "-" with `from: [stdin]`.
func acquiresValue(fd FlagDef, value string) bool {
	switch {
	case strings.HasPrefix(value, "@@"):
		return false
	case strings.HasPrefix(value, "@"):
		return slices.Contains(fd.From, "file")
	case value == "-":
		return slices.Contains(fd.From, "stdin")
	}
	return false
}

// splitsAcquiredLines reports whether fd takes a file's or stdin's lines as separate values:
// a list or map flag that is not object-valued (an object's file is one document).
func splitsAcquiredLines(fd FlagDef) bool {
	return fd.ObjectSchema == "" && (strings.HasPrefix(fd.Type, "[]") || strings.HasPrefix(fd.Type, "map["))
}

// splitAcquired splits the content of a list or map flag's `@file` or "-" into values: its
// lines (see acquiredLines; NUL-separated items with a NUL separator), each split further on the
// flag's separator.
func splitAcquired(content, sep string) ([]string, error) {
	nul := sep == "\x00"
	items := acquiredLines(content, nul, false)
	if sep == "" || nul {
		return items, nil
	}
	var values []string
	for _, item := range items {
		vals, err := splitValue(item, sep)
		if err != nil {
			return nil, err
		}
		values = append(values, vals...)
	}
	return values, nil
}
