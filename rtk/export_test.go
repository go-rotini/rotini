package rtk

// DetectColorLevelForTest is a test-only wrapper around the
// unexported [detectColorLevel] function. The internal rule table
// is hard to exercise through [NewTerm] (positive-color cases
// require an actual TTY writer, which tests can't reliably
// fabricate), so we expose the rule logic directly for unit testing.
//
// Production code never uses this — the name carries "ForTest" so
// any accidental import outside _test.go files is loud.
func DetectColorLevelForTest(isTTY bool, noColor, term, colorterm string) ColorLevel {
	return detectColorLevel(isTTY, noColor, term, colorterm)
}
