package rotini

import "testing"

// FLAG-27: digits right after a count or bool flag in a cluster, with no digit flag declared,
// are a value the flag doesn't take: -v3 says to repeat -v.
func confDigitsAfterSwitch(t *testing.T, _ *Context, _ InputSettings) {
	for argv, want := range map[string]string{
		"-v3": `-v counts occurrences and takes no value; repeat it instead (-vvv), not "-v3"`,
		"-a3": `-a takes no value (got "-a3")`,
	} {
		err := NewParser().Parse(NewContextFor(wordingDef(), []string{argv}), &struct{}{})
		if err == nil || err.Error() != want || CategoryOf(err) != CategoryUsage {
			t.Errorf("%s: err = %v, want the usage error %q", argv, err, want)
		}
	}
}
