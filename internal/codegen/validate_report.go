package codegen

import (
	"errors"
	"strconv"
	"strings"
)

// ProblemRecord is one validate finding as data, for `rotini validate --format json`.
type ProblemRecord struct {
	Severity string // "error" or "warning"
	Document string // "spec" or "conf"; "" when the finding is about neither
	File     string // the document's path; "" when unknown
	Line     int    // 1-based; 0 when the finding has no position
	Col      int    // 1-based; 0 when the finding has no position
	Pointer  string // the JSON pointer of the key at fault; "" when unknown
	Message  string // what is wrong
	Hint     string // what to do about it; "" when the message says nothing more
}

// ProblemRecordOf describes err, an error or a warning from validate. Every rotini message
// reads "<what is wrong>; <what to do>", so the text after the first "; " is the hint, and
// Message plus "; " plus Hint is the line the text report prints.
func ProblemRecordOf(err error, warning bool) ProblemRecord {
	r := ProblemRecord{Severity: "error"}
	if warning {
		r.Severity = "warning"
	}
	var p *problem
	if !errors.As(err, &p) {
		r.Message, r.Hint = splitHint(err.Error())
		return r
	}
	r.Document = p.kind
	r.Message, r.Hint = splitHint(p.msg)
	if p.loc != "" && !strings.HasPrefix(p.loc, "/") {
		r.Message = p.loc + ": " + r.Message // the label the text report shows: "command taskr/add"
	}
	r.Pointer = p.ptr
	if r.Pointer == "" && strings.HasPrefix(p.loc, "/") {
		r.Pointer = p.loc
	}
	r.File, r.Line, r.Col = splitPosition(p.pos)
	return r
}

// splitHint splits a message at its first "; " into what is wrong and what to do.
func splitHint(msg string) (string, string) {
	if what, fix, ok := strings.Cut(msg, "; "); ok {
		return what, fix
	}
	return msg, ""
}

// splitPosition splits "path:line:col" into its parts. A bare path has no line or column.
func splitPosition(pos string) (string, int, int) {
	rest, colText, ok := strings.CutLast(pos, ":")
	if !ok {
		return pos, 0, 0
	}
	file, lineText, ok := strings.CutLast(rest, ":")
	line, err1 := strconv.Atoi(lineText)
	col, err2 := strconv.Atoi(colText)
	if !ok || err1 != nil || err2 != nil {
		return pos, 0, 0
	}
	return file, line, col
}
