package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCreateOutput_categories pins that a missing directory and a denied permission are usage
// errors, and that an uncategorised I/O failure stays CategoryNone.
func TestCreateOutput_categories(t *testing.T) {
	dir := t.TempDir()
	rtx := NewContextFor(Definition{Name: "app"}, nil).WithDir(dir)
	if _, err := CreateOutput(rtx, filepath.Join("missing", "out.txt")); CategoryOf(err) != CategoryUsage {
		t.Errorf("missing directory: %v, category %v", err, CategoryOf(err))
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		if _, err := CreateOutput(rtx, filepath.Join("locked", "out.txt")); CategoryOf(err) != CategoryUsage {
			t.Errorf("denied: %v, category %v", err, CategoryOf(err))
		}
	}
	if err := outputOpenError("could not create output file", errors.New("disk on fire")); CategoryOf(err) != CategoryNone {
		t.Errorf("other I/O failure: category %v", CategoryOf(err))
	}
}

// TestAppDirsAndArgvOf_internal pins that AppDirs and ArgvOf errors are internal, and that an
// ArgvError unwraps to ErrInternal.
func TestAppDirsAndArgvOf_internal(t *testing.T) {
	if _, err := AppDirs(nil, "app", "bogus"); !errors.Is(err, ErrInternal) {
		t.Errorf("AppDirs: %v", err)
	}
	var ae error = &ArgvError{Field: "App.Flags.X", Reason: "no"}
	if !errors.Is(ae, ErrInternal) || CategoryOf(ae) != CategoryInternal {
		t.Errorf("ArgvError category %v", CategoryOf(ae))
	}
	type notInputs struct{ X int }
	if _, _, err := ArgvOf(Definition{Name: "app"}, notInputs{}, nil); !errors.Is(err, ErrInternal) {
		t.Errorf("ArgvOf: %v", err)
	}
}
