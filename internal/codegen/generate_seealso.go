package codegen

import (
	"slices"
	"strings"
)

// seeAlsoEntry is one `see_also` item: text as written, or a reference to one of the program's
// commands, a help topic or a URL.
type seeAlsoEntry struct {
	text    string
	command []string // the command's names below the root of the spec that wrote it; nil for none
	topic   string
	url     string

	// rooted marks command as the path below the program's root, rebased from a composed
	// spec's own root.
	rooted bool
}

// seeAlsoOf reads a `see_also` list: a string, a {command|topic|url} object, or an entry
// already rebased by [program.rebaseSeeAlso]. An empty list stays empty, not nil, since an
// empty list written beside a `$ref` clears the composed command's.
func seeAlsoOf(list []any) []seeAlsoEntry {
	if list == nil {
		return nil
	}
	out := make([]seeAlsoEntry, 0, len(list))
	for _, item := range list {
		var e seeAlsoEntry
		switch v := item.(type) {
		case string:
			e.text = v
		case seeAlsoEntry:
			e = v
		case SeeAlso:
			e = seeAlsoRef(v)
		case *SeeAlso:
			if v == nil {
				continue
			}
			e = seeAlsoRef(*v)
		case map[string]any:
			command, _ := v["command"].(string)
			topic, _ := v["topic"].(string)
			url, _ := v["url"].(string)
			e = seeAlsoRef(SeeAlso{Command: command, Topic: topic, Url: url})
		default:
			continue
		}
		out = append(out, e)
	}
	return out
}

// seeAlsoRef is the entry a {command|topic|url} object makes.
func seeAlsoRef(r SeeAlso) seeAlsoEntry {
	e := seeAlsoEntry{topic: r.Topic, url: r.Url}
	if r.Command != "" {
		e.command = strings.Fields(r.Command)
	}
	return e
}

// rebaseSeeAlso returns list with each command reference that is relative to the root of the
// spec mounted at specRoot (an underscore path; "" for the program's own spec) made relative
// to the program's root, so a composed spec's `{command: add}` names its own `add`.
func (gp *program) rebaseSeeAlso(list []any, specRoot string) []any {
	if list == nil || specRoot == "" {
		return list
	}
	out := make([]any, 0, len(list))
	for _, e := range seeAlsoOf(list) {
		if e.command != nil && !e.rooted {
			e.command = slices.Concat(gp.pathSegments[specRoot], e.command)
			e.rooted = true
		}
		out = append(out, e)
	}
	return out
}

// templateDocSeeAlso is one `see_also` entry on a page.
type templateDocSeeAlso struct {
	Text string // as help shows it: the text as written, a command line, `<root> help <topic>`, or the URL
	Page string // a command's or topic's page name (taskr-remote-add); "" for text and URLs
	URL  string // the URL of a url entry; "" otherwise
}

// seeAlsoRows renders a command's `see_also` entries for the pages, naming the program's own
// pages after rootName and showing command lines with rootDisplay.
func seeAlsoRows(entries []seeAlsoEntry, rootName, rootDisplay string) []templateDocSeeAlso {
	if len(entries) == 0 {
		return nil
	}
	out := make([]templateDocSeeAlso, 0, len(entries))
	for _, e := range entries {
		var row templateDocSeeAlso
		switch {
		case e.command != nil:
			row = templateDocSeeAlso{Text: strings.Join(append([]string{rootDisplay}, e.command...), " "), Page: manPageName(rootName, e.command)}
		case e.topic != "":
			row = templateDocSeeAlso{Text: rootDisplay + " help " + e.topic, Page: manPageName(rootName, []string{e.topic})}
		case e.url != "":
			row = templateDocSeeAlso{Text: e.url, URL: e.url}
		default:
			row = templateDocSeeAlso{Text: e.text}
		}
		out = append(out, row)
	}
	return out
}

// sanitizeSeeAlso returns copies of a page's see_also texts and rows with their text cleaned.
func sanitizeSeeAlso(texts []string, rows []templateDocSeeAlso, clean func(string) string) ([]string, []templateDocSeeAlso) {
	texts = append([]string(nil), texts...)
	for i := range texts {
		texts[i] = clean(texts[i])
	}
	rows = append([]templateDocSeeAlso(nil), rows...)
	for i := range rows {
		rows[i].Text = clean(rows[i].Text)
	}
	return texts, rows
}

// seeAlsoTexts is each row's text, the form a template reading .SeeAlso as strings shows.
func seeAlsoTexts(rows []templateDocSeeAlso) []string {
	if rows == nil {
		return nil
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Text
	}
	return out
}
