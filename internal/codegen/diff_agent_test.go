package codegen

import "testing"

func TestDiff_effectsAndAgentRules(t *testing.T) {
	sub := func(lines string) string { return "  commands:\n    - name: rm\n" + lines }
	flag := func(lines string) string {
		return sub("      effects: {kind: write}\n      flags:\n        - name: force\n          identifiers: [--force]\n" + lines +
			"          schema: {type: bool}\n")
	}
	runDiffCases(t, []diffCase{
		{
			name: "command effects declared", old: sub(""), new: sub("      effects: {kind: write}\n"),
			want: []string{"safe EFFECTS_ADDED app rm"},
		},
		{
			name: "command effects removed", old: sub("      effects: {kind: read}\n"), new: sub(""),
			want: []string{"possibly_breaking EFFECTS_REMOVED app rm"},
		},
		{
			name: "kind raised", old: sub("      effects: {kind: write}\n"), new: sub("      effects: {kind: destructive}\n"),
			want: []string{"possibly_breaking EFFECTS_RAISED app rm"},
		},
		{
			name: "no longer idempotent", old: sub("      effects: {kind: write, idempotent: true}\n"), new: sub("      effects: {kind: write}\n"),
			want: []string{"possibly_breaking EFFECTS_RAISED app rm"},
		},
		{
			name: "no longer local", old: sub("      effects: {kind: read, open_world: false}\n"), new: sub("      effects: {kind: read, open_world: true}\n"),
			want: []string{"possibly_breaking EFFECTS_RAISED app rm"},
		},
		{
			name: "lowered", old: sub("      effects: {kind: destructive}\n"), new: sub("      effects: {kind: read, idempotent: true, open_world: false}\n"),
			want: []string{"safe EFFECTS_LOWERED app rm"},
		},
		{
			name: "raised and lowered", old: sub("      effects: {kind: read, idempotent: true}\n"), new: sub("      effects: {kind: write, idempotent: true, open_world: false}\n"),
			want: []string{"possibly_breaking EFFECTS_RAISED app rm", "safe EFFECTS_LOWERED app rm"},
		},
		{
			name: "flag effects raised", old: flag("          effects: {kind: write}\n"), new: flag("          effects: {kind: destructive}\n"),
			want: []string{"possibly_breaking EFFECTS_RAISED app rm --force"},
		},
		{
			name: "flag effects removed", old: flag("          effects: {kind: destructive}\n"), new: flag(""),
			want: []string{"possibly_breaking EFFECTS_REMOVED app rm --force"},
		},
		{
			name: "experimental command", old: sub("      stability: experimental\n      effects: {kind: read}\n"), new: sub("      stability: experimental\n"),
			want: []string{"safe EFFECTS_REMOVED app rm"},
		},
		{
			name: "command kept from agents", old: sub(""), new: sub("      agent: false\n"),
			want: []string{"possibly_breaking AGENT_REMOVED app rm"},
		},
		{
			name: "command no longer brought in", old: sub("      agent: true\n"), new: sub(""),
			want: []string{"possibly_breaking AGENT_REMOVED app rm"},
		},
		{
			name: "command offered again", old: sub("      agent: false\n"), new: sub(""),
			want: []string{"safe AGENT_ADDED app rm"},
		},
		{
			name: "command brought in", old: sub(""), new: sub("      agent: true\n"),
			want: []string{"safe AGENT_ADDED app rm"},
		},
		{
			name: "flag kept from agents", old: flag(""), new: flag("          agent: false\n"),
			want: []string{"possibly_breaking AGENT_REMOVED app rm --force"},
		},
		{
			name: "env kept from agents",
			old:  "  env:\n    - name: token\n      schema: {type: string, variable: APP_TOKEN}\n",
			new:  "  env:\n    - name: token\n      agent: false\n      schema: {type: string, variable: APP_TOKEN}\n",
			want: []string{"possibly_breaking AGENT_REMOVED app $APP_TOKEN"},
		},
	})
}

func TestDiff_roleRules(t *testing.T) {
	format := func(lines string) string {
		return "  flags:\n    - name: format\n      identifiers: [--format]\n" + lines + "      schema: {type: string, enum: [text, json]}\n"
	}
	runDiffCases(t, []diffCase{
		{
			name: "role declared", old: format(""), new: format("      role: machine-output\n      role_value: json\n"),
			want: []string{"safe FLAG_ROLE_ADDED app --format", "safe FLAG_ROLE_VALUE_CHANGED app --format"},
		},
		{
			name: "role removed", old: format("      role: machine-output\n      role_value: json\n"), new: format(""),
			want: []string{"possibly_breaking FLAG_ROLE_CHANGED app --format", "possibly_breaking FLAG_ROLE_VALUE_CHANGED app --format"},
		},
		{
			name: "role value changed", old: format("      role: machine-output\n      role_value: json\n"), new: format("      role: machine-output\n      role_value: text\n"),
			want: []string{"possibly_breaking FLAG_ROLE_VALUE_CHANGED app --format"},
		},
	})
}

func TestDiff_profileRules(t *testing.T) {
	spec := func(profiles, flagSchema string) string {
		s := "  config_files:\n    - name: app\n      path: ./app.yaml\n"
		if profiles != "" {
			s += "      profiles: " + profiles + "\n"
		}
		return s + "  flags:\n    - name: profile\n      identifiers: [--profile]\n      cascading: true\n      schema: " + flagSchema + "\n" +
			"    - name: context\n      identifiers: [--context]\n      cascading: true\n      schema: {type: string}\n"
	}
	plain := "{type: string}"
	withVar := "{type: string, variable: APP_PROFILE}"
	files := "  config_files:\n    - name: app\n      path: ./app.yaml\n      profiles: {under: profiles, select: profile}\n"
	runDiffCases(t, []diffCase{
		{
			name: "added", old: spec("", plain), new: spec("{under: profiles, select: profile}", plain),
			want: []string{"safe PROFILES_ADDED app config_files app"},
		},
		{
			name: "added with a default", old: spec("", plain), new: spec("{under: profiles, select: profile, default: dev}", plain),
			want: []string{"possibly_breaking PROFILES_ADDED app config_files app"},
		},
		{
			name: "removed", old: spec("{under: profiles, select: profile}", plain), new: spec("", plain),
			want: []string{"breaking PROFILES_NO_DELETE app config_files app"},
		},
		{
			name: "under changed", old: spec("{under: profiles, select: profile}", plain), new: spec("{under: contexts, select: profile}", plain),
			want: []string{"breaking PROFILES_UNDER_CHANGED app config_files app"},
		},
		{
			name: "selector flag changed", old: spec("{under: profiles, select: profile}", plain), new: spec("{under: profiles, select: context}", plain),
			want: []string{"breaking PROFILES_FLAG_NO_DELETE app config_files app"},
		},
		{
			name: "selector variable added", old: spec("{under: profiles, select: profile}", plain), new: spec("{under: profiles, select: profile}", withVar),
			want: []string{"safe PROFILES_ENV_ADDED app config_files app $APP_PROFILE", "safe INPUT_ENV_ADDED app --profile $APP_PROFILE"},
		},
		{
			name: "selector variable removed", old: spec("{under: profiles, select: profile}", withVar), new: spec("{under: profiles, select: profile}", plain),
			want: []string{"breaking PROFILES_ENV_NO_DELETE app config_files app $APP_PROFILE", "breaking INPUT_ENV_NO_DELETE app --profile $APP_PROFILE"},
		},
		{
			name: "default changed", old: spec("{under: profiles, select: profile, default: dev}", plain), new: spec("{under: profiles, select: profile, default: prod}", plain),
			want: []string{"possibly_breaking PROFILES_DEFAULT_CHANGED app config_files app"},
		},
		{
			name: "selector flag added",
			old:  files + "  env:\n    - name: profile\n      schema: {type: string, variable: APP_PROFILE}\n",
			new: files + "  env:\n    - name: profile\n      schema: {type: string, variable: APP_PROFILE}\n" +
				"  flags:\n    - name: profile\n      identifiers: [--profile]\n      cascading: true\n      schema: {type: string}\n",
			want: []string{"safe FLAG_ADDED app --profile", "safe PROFILES_FLAG_ADDED app config_files app"},
		},
	})
}
