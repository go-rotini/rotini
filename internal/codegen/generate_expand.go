package codegen

import (
	"strconv"
	"strings"
)

// expansionTags renders an input's `expand` and `relative_to` as the struct tags the input
// reader applies (`expand:"home,env" relativeto:"config"`), or "" when it declares neither.
func expansionTags(schema *InputSchema) string {
	if schema == nil {
		return ""
	}
	var parts []string
	if len(schema.Expand) > 0 {
		parts = append(parts, "expand:"+strconv.Quote(strings.Join(schema.Expand, ",")))
	}
	if schema.RelativeTo != "" {
		parts = append(parts, "relativeto:"+strconv.Quote(schema.RelativeTo))
	}
	return strings.Join(parts, " ")
}

// pathTag marks an env or config field of type existingfile or existingdir (or a list of
// either) with `path:"file"` or `path:"dir"`, since its Go type is a plain string and the input
// reader checks the path only when told to.
func pathTag(schema *InputSchema) string {
	switch strings.TrimPrefix(definitionType(schema, nil), "[]") {
	case "existingfile":
		return `path:"file"`
	case "existingdir":
		return `path:"dir"`
	}
	return ""
}

// joinTags joins the non-empty struct-tag fragments with spaces.
func joinTags(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}
