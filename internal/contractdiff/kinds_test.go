package contractdiff

import "testing"

// kindsOutput is a stream command whose items come in the given kinds.
func kindsOutput(kinds string) string {
	return root(`"stream": true, "output": {"oneOf": [` + kinds + `]}`)
}

// kind is an inline kind tagged by tag = value, with more properties.
func kind(tag, value, props string) string {
	if props != "" {
		props = ", " + props
	}
	return `{"type": "object", "properties": {"` + tag + `": {"type": "string", "const": "` + value + `"}` + props + `}, "required": ["` + tag + `"]}`
}

// TestDiff_outputKinds pins the rules for a stream whose items come in several kinds: kinds
// match by tag value, whatever their order; adding one is safe; removing one, changing its
// value or renaming the tag breaks readers; a kept kind follows the output rules.
func TestDiff_outputKinds(t *testing.T) {
	progress := kind("kind", "progress", `"percent": {"type": "integer"}`)
	result := kind("kind", "result", `"ok": {"type": "boolean"}`)
	runRaw(t, []rawCase{
		{
			name: "reordered kinds",
			old:  contract(kindsOutput(progress + ", " + result)),
			new:  contract(kindsOutput(result + ", " + progress)),
		},
		{
			name: "a kind added",
			old:  contract(kindsOutput(progress)),
			new:  contract(kindsOutput(progress + ", " + result)),
			want: []string{"safe OUTPUT_KIND_ADDED app output[kind=result]"},
		},
		{
			name: "a kind removed",
			old:  contract(kindsOutput(progress + ", " + result)),
			new:  contract(kindsOutput(progress)),
			want: []string{"breaking OUTPUT_KIND_NO_DELETE app output[kind=result]"},
		},
		{
			name: "a kind's value changed",
			old:  contract(kindsOutput(progress + ", " + result)),
			new:  contract(kindsOutput(progress + ", " + kind("kind", "done", `"ok": {"type": "boolean"}`))),
			want: []string{"breaking OUTPUT_KIND_NO_DELETE app output[kind=result]", "safe OUTPUT_KIND_ADDED app output[kind=done]"},
		},
		{
			name: "the tag renamed",
			old:  contract(kindsOutput(progress + ", " + result)),
			new:  contract(kindsOutput(kind("type", "progress", "") + ", " + kind("type", "result", ""))),
			want: []string{"breaking OUTPUT_KIND_TAG_CHANGED app output"},
		},
		{
			name: "inside a kind",
			old:  contract(kindsOutput(progress + ", " + result)),
			new:  contract(kindsOutput(kind("kind", "progress", `"percent": {"type": "string"}, "eta": {"type": "string"}`) + ", " + kind("kind", "result", ""))),
			want: []string{
				"breaking OUTPUT_TYPE_CHANGED app output[kind=progress].percent",
				"safe OUTPUT_PROPERTY_ADDED app output[kind=progress].eta",
				"breaking OUTPUT_PROPERTY_NO_DELETE app output[kind=result].ok",
			},
		},
		{
			name: "a named kind",
			old:  contract(kindsOutput(progress+`, {"$ref": "#/definitions/Result"}`), `"definitions": {"Result": `+result+`}`),
			new:  contract(kindsOutput(progress+`, {"$ref": "#/definitions/Result"}`), `"definitions": {"Result": `+kind("kind", "result", "")+`}`),
			want: []string{"breaking OUTPUT_PROPERTY_NO_DELETE definitions.Result.ok"},
		},
		{
			name: "one shape becomes several kinds",
			old:  contract(root(`"stream": true, "output": ` + progress)),
			new:  contract(kindsOutput(progress + ", " + result)),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app output"},
		},
		{
			name: "several kinds become one shape",
			old:  contract(kindsOutput(progress + ", " + result)),
			new:  contract(root(`"stream": true, "output": ` + progress)),
			want: []string{"breaking OUTPUT_TYPE_CHANGED app output"},
		},
	})
}
