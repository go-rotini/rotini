package rotini

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/go-rotini/rotini/internal/codegen"
)

func TestCLI_mount(t *testing.T) {
	into := "cmd/app/.rotini.spec.yaml"
	mounted := codegen.MountResult{
		Mounted: []codegen.MountEntry{{Name: "db", Ref: "../db/.rotini.spec.yaml"}},
		Already: []codegen.MountEntry{{Name: "web", Ref: "../web/.rotini.spec.yaml"}},
		Removed: []string{"../old/.rotini.spec.yaml"},
		Skipped: []string{into},
	}
	tests := []struct {
		name   string
		argv   []string
		res    codegen.MountResult
		err    error
		code   int
		stdout string
		stderr string
	}{
		{"mounts", []string{"mount", "a.yaml", "--into", into, "--group", "tools", "--prune"}, mounted, nil, 0,
			"mounted db from ../db/.rotini.spec.yaml\n" +
				"removed ../old/.rotini.spec.yaml: the file no longer exists\n" +
				"skipped cmd/app/.rotini.spec.yaml: it is the spec being mounted into\n" +
				"cmd/app/.rotini.spec.yaml: 1 mounted, 1 already mounted, 1 removed\n", ""},
		{"dry run, a change", []string{"mount", "a.yaml", "--into", into, "-n"}, mounted, nil, 2,
			"would mount db from ../db/.rotini.spec.yaml\n" +
				"would remove ../old/.rotini.spec.yaml: the file no longer exists\n" +
				"skipped cmd/app/.rotini.spec.yaml: it is the spec being mounted into\n" +
				"cmd/app/.rotini.spec.yaml: 1 would mount, 1 already mounted, 1 would remove\n", ""},
		{"dry run, nothing to do", []string{"mount", "a.yaml", "--into", into, "--dry-run"},
			codegen.MountResult{Already: mounted.Already}, nil, 0,
			"cmd/app/.rotini.spec.yaml: 0 would mount, 1 already mounted, 0 would remove\n", ""},
		{"an error", []string{"mount", "a.yaml", "--into", into}, codegen.MountResult{},
			errors.Join(errors.New("a.yaml: one"), errors.New("b.yaml: two")), 1, "",
			"Error: a.yaml: one\nError: b.yaml: two\n"},
		{"under", []string{"mount", "a.yaml", "--into", into, "--under", "db  migrate"}, codegen.MountResult{Mounted: mounted.Mounted}, nil, 0,
			"mounted db from ../db/.rotini.spec.yaml\n" +
				"cmd/app/.rotini.spec.yaml, under db migrate: 1 mounted, 0 already mounted, 0 removed\n", ""},
		{"nothing named", []string{"mount", "--into", into}, codegen.MountResult{}, nil, 1, "",
			"Error: name a spec to mount, or pass --prune\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, out, errb := newTestCLI(t)
			var gotSpecs []string
			var gotOpts codegen.MountOptions
			prog.WithDependency(mountDep, codegen.MountFn(func(in string, specs []string, opts codegen.MountOptions) (codegen.MountResult, error) {
				if in != into {
					t.Errorf("into = %q", in)
				}
				gotSpecs, gotOpts = specs, opts
				return tt.res, tt.err
			}))
			code, _ := prog.Run(tt.argv)
			if code != tt.code {
				t.Errorf("exit = %d, want %d (stderr %q)", code, tt.code, errb.String())
			}
			if out.String() != tt.stdout || errb.String() != tt.stderr {
				t.Errorf("stdout %q, stderr %q; want %q, %q", out.String(), errb.String(), tt.stdout, tt.stderr)
			}
			if gotSpecs != nil && !slices.Equal(gotSpecs, []string{"a.yaml"}) {
				t.Errorf("specs = %v", gotSpecs)
			}
			under := []string{}
			if i := slices.Index(tt.argv, "--under"); i >= 0 {
				under = []string{"db", "migrate"}
			}
			wantOpts := codegen.MountOptions{
				Under:  under,
				Group:  map[bool]string{true: "tools"}[slices.Contains(tt.argv, "--group")],
				Prune:  slices.Contains(tt.argv, "--prune"),
				DryRun: slices.Contains(tt.argv, "-n") || slices.Contains(tt.argv, "--dry-run"),
			}
			if gotSpecs != nil && !reflect.DeepEqual(gotOpts, wantOpts) {
				t.Errorf("opts = %+v, want %+v", gotOpts, wantOpts)
			}
		})
	}
}
