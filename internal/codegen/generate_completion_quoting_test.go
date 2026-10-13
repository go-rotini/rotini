package codegen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hostileCandidates are values a dynamic completer could list from outside data. Each must
// reach the program as one literal word, whatever shell inserts it.
var hostileCandidates = []string{
	"a b", "a,b", "it's $HOME", `say "hi"`, "`touch pwned`", "$(touch pwned)", "x;touch pwned",
	"star*", "[ab]", "~root", "-dash", "x:y", "café", "o’neil;touch pwned", "‘q’", "“q”",
	`back\slash`, "a!b", "ends'", `ends"`, "ends!", "{a,b}", "a&b|c<d>e", "#hash", "tab?",
	"x’;touch pwned;’", "x‘;touch pwned;‘", "u@host", "-foo.bar", "-Dkey=a.b", "--%",
}

// completionQuotingDir returns a directory holding a fake "my-prog" that answers __complete
// with the lines of $CANDS and records the words it was given in $ARGS_OUT. Run any other way,
// it prints an ARG line for each argument it receives.
func completionQuotingDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	fake := "#!/bin/sh\nif [ \"$1\" != __complete ]; then for a; do printf 'ARG %s\\n' \"$a\"; done; exit 0; fi\n" +
		"echo >> calls.txt\nshift\nprintf '%s\\n' \"$@\" > \"$ARGS_OUT\"\ncat \"$CANDS\"\n"
	if err := os.WriteFile(filepath.Join(dir, "my-prog"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// bashQuotingDriver completes one line the way readline's TAB does and prints, for each
// candidate, the words the line then holds after the program (or INCOMPLETE when the shell
// would wait for more), and the same for the text the candidates share.
// Usage: bash driver.sh <script> <kept text> <open quote> <COMP_LINE> <COMP_WORDS...>
const bashQuotingDriver = `source "$1"
keep=$2 quote=$3 COMP_LINE=$4
shift 4
COMP_POINT=${#COMP_LINE} COMP_WORDS=("$@") COMP_CWORD=$(( $# - 1 )) COMP_TYPE=9 COMPREPLY=()
fn=$(complete -p my-prog | sed -n 's/.* -F \([^ ]*\) .*/\1/p')
"$fn" >/dev/null 2>&1
parse() {
    local w
    if ! bash -n -c "w=($1)" 2>/dev/null; then
        echo INCOMPLETE
        return
    fi
    eval "w=($1)"
    printf 'WORD %s\n' "${w[@]:1}"
}
for v in "${COMPREPLY[@]}"; do
    full=$keep$v
    if [[ -n $quote && $v != *"$quote" ]]; then full+=$quote; fi
    echo INSERT
    parse "$full"
done
if (( ${#COMPREPLY[@]} > 1 )); then
    lcd=${COMPREPLY[0]}
    for v in "${COMPREPLY[@]}"; do
        while [[ ${v:0:${#lcd}} != "$lcd" ]]; do lcd=${lcd%?}; done
    done
    echo SHARED
    parse "$keep$lcd"
fi
`

type quotingCase struct {
	name  string
	line  string     // COMP_LINE: what the user typed
	words [][]string // COMP_WORDS as bash 3.2 and bash 4 or later split line
	keep  string     // the line before the text readline replaces
	quote string     // the quote the current word leaves open
	fixed []string   // the words between the program and the completed one
	sent  string     // the current word as the program must receive it
	cands []string
}

var bashQuotingCases = []quotingCase{
	{
		name: "bare", line: "my-prog --v ", words: [][]string{{"my-prog", "--v", ""}},
		keep: "my-prog --v ", fixed: []string{"--v"}, cands: hostileCandidates,
	},
	{
		name: "shared prefix", line: "my-prog --v x", words: [][]string{{"my-prog", "--v", "x"}},
		keep: "my-prog --v ", fixed: []string{"--v"}, sent: "x",
		cands: []string{"x;touch pwned", "x;touch pwnedz", "x a", "x,b", "x’a", "x”b", "x$(touch pwned)", "x$HOME"},
	},
	{
		name: "after =", line: "my-prog --v=", words: [][]string{{"my-prog", "--v="}, {"my-prog", "--v", "="}},
		keep: "my-prog --v=", sent: "--v=",
		cands: prefixed("--v=", hostileCandidates),
	},
	{
		name: "after :", line: "my-prog host:", words: [][]string{{"my-prog", "host:"}, {"my-prog", "host", ":"}},
		keep: "my-prog host:", sent: "host:",
		cands: []string{"host:8080", "host:a b", "host:x;touch pwned", "host:~root"},
	},
	{
		name: "invalid UTF-8", line: "my-prog --v x", words: [][]string{{"my-prog", "--v", "x"}},
		keep: "my-prog --v ", fixed: []string{"--v"}, sent: "x",
		cands: []string{"x\xc3", "xé", "x\xc3\xa9\xff"},
	},
	{
		name: "invalid UTF-8 in single quotes", line: "my-prog --v 'x", words: [][]string{{"my-prog", "--v", "'x"}},
		keep: "my-prog --v '", quote: "'", fixed: []string{"--v"}, sent: "x",
		cands: []string{"x\xc3", "xé"},
	},
	{
		name: "invalid UTF-8 in double quotes", line: `my-prog --v "x`, words: [][]string{{"my-prog", "--v", `"x`}},
		keep: `my-prog --v "`, quote: `"`, fixed: []string{"--v"}, sent: "x",
		cands: []string{"x\xc3", "xé"},
	},
	{
		name: "open single quote", line: "my-prog --v 'i", words: [][]string{{"my-prog", "--v", "'i"}},
		keep: "my-prog --v '", quote: "'", fixed: []string{"--v"}, sent: "i",
		cands: []string{"it's", "it'", "it!", `it"x`, "i$(touch pwned)", "i`touch pwned`", "i b"},
	},
	{
		name: "open double quote", line: `my-prog --v "s`, words: [][]string{{"my-prog", "--v", `"s`}},
		keep: `my-prog --v "`, quote: `"`, fixed: []string{"--v"}, sent: "s",
		cands: []string{`say "hi"`, "s$HOME", "s!", `s"`, "s`touch pwned`", `s\x`, "s$(touch pwned)", "s!x", "s'"},
	},
	{
		name: "backslash typed", line: `my-prog --v it\'`, words: [][]string{{"my-prog", "--v", `it\'`}},
		keep: "my-prog --v ", fixed: []string{"--v"}, sent: "it'",
		cands: []string{"it's", "it's me"},
	},
	{
		name: "open quote after =", line: "my-prog --v='i", words: [][]string{{"my-prog", "--v='i"}, {"my-prog", "--v", "=", "'i"}},
		keep: "my-prog --v='", quote: "'", sent: "--v=i",
		cands: []string{"--v=it's", "--v=i b", "--v=i$(touch pwned)"},
	},
}

func prefixed(prefix string, vals []string) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = prefix + v
	}
	return out
}

// TestCompletionScript_bashInsertsLiteralWords completes hostile candidates with the bash script,
// one at a time and all together, and parses what readline would insert: each candidate must
// come back as exactly one word holding its value, and the text several share must be a
// complete word (or an open quote the user is already in) that runs nothing.
func TestCompletionScript_bashInsertsLiteralWords(t *testing.T) {
	t.Parallel()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := completionScript("my-prog", "bash", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	dir := completionQuotingDir(t)
	scriptPath := filepath.Join(dir, "completion.bash")
	driverPath := filepath.Join(dir, "driver.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(driverPath, []byte(bashQuotingDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range bashQuotingCases {
		sets := [][]string{tc.cands}
		for _, c := range tc.cands {
			sets = append(sets, []string{c})
		}
		for _, words := range tc.words {
			for _, cands := range sets {
				out := runQuotingDriver(t, dir, cands, bash, append([]string{driverPath, scriptPath, tc.keep, tc.quote, tc.line}, words...)...)
				checkInserted(t, tc, words, cands, out)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("a candidate ran a command")
	}
}

// runQuotingDriver runs a driver in dir with cands as the fake program's answer and returns its
// output, and the arguments the program was given after __complete.
func runQuotingDriver(t *testing.T, dir string, cands []string, name string, args ...string) quotingRun {
	t.Helper()
	candsPath := filepath.Join(dir, "cands.txt")
	argsPath := filepath.Join(dir, "args.txt")
	if err := os.WriteFile(candsPath, []byte(strings.Join(cands, "\n")+"\n:rotini:none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(argsPath)
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CANDS="+candsPath, "ARGS_OUT="+argsPath, "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", name, err, out)
	}
	sent, _ := os.ReadFile(argsPath)
	return quotingRun{out: string(out), sent: strings.Split(strings.TrimSuffix(string(sent), "\n"), "\n")}
}

type quotingRun struct {
	out  string   // the driver's report
	sent []string // what the program received after __complete
}

// checkInserted checks a driver's report: INSERT blocks in candidate order, then SHARED.
func checkInserted(t *testing.T, tc quotingCase, words, cands []string, run quotingRun) {
	t.Helper()
	if got := run.sent[len(run.sent)-1]; got != tc.sent {
		t.Errorf("%s %q: the program got the current word %q, want %q", tc.name, words, got, tc.sent)
	}
	blocks := strings.Split(run.out, "\n")
	var inserts [][]string
	var shared []string
	var cur *[]string
	sharedSeen, incomplete := false, false
	for _, line := range blocks {
		switch {
		case line == "INSERT":
			inserts = append(inserts, []string{})
			cur = &inserts[len(inserts)-1]
		case line == "SHARED":
			sharedSeen = true
			cur = &shared
		case line == "INCOMPLETE":
			if sharedSeen {
				incomplete = true
			} else {
				*cur = append(*cur, "<incomplete>")
			}
		case strings.HasPrefix(line, "WORD "):
			*cur = append(*cur, strings.TrimPrefix(line, "WORD "))
		}
	}
	if len(inserts) != len(cands) {
		t.Errorf("%s %q with %d candidates: %d inserted\n%s", tc.name, words, len(cands), len(inserts), run.out)
		return
	}
	for i, c := range cands {
		want := append(slices.Clone(tc.fixed), c)
		if !slices.Equal(inserts[i], want) {
			t.Errorf("%s %q: inserting %q gives the words %q, want %q", tc.name, words, c, inserts[i], want)
		}
	}
	if len(cands) < 2 {
		return
	}
	if incomplete {
		if tc.quote == "" {
			t.Errorf("%s %q: the text %d candidates share leaves the line incomplete", tc.name, words, len(cands))
		}
		return
	}
	if slices.Equal(shared, tc.fixed) {
		return // nothing shared: readline keeps what was typed
	}
	if len(shared) != len(tc.fixed)+1 || !slices.Equal(shared[:len(tc.fixed)], tc.fixed) {
		t.Errorf("%s %q: the shared text gives the words %q", tc.name, words, shared)
		return
	}
	for _, c := range cands {
		if !strings.HasPrefix(c, shared[len(shared)-1]) {
			t.Errorf("%s %q: the shared text gives %q, which %q doesn't start with", tc.name, words, shared[len(shared)-1], c)
		}
	}
}

// TestCompletionScript_bashQuotesManyCandidatesInLinearTime completes 5,000 candidates that
// share a prefix and bounds the CPU time bash spends. bash 3.2 finds an array element by
// walking the array, so indexing or appending per candidate makes TAB take seconds.
func TestCompletionScript_bashQuotesManyCandidatesInLinearTime(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("times bash")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	script, err := completionScript("my-prog", "bash", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	dir := completionQuotingDir(t)
	scriptPath := filepath.Join(dir, "completion.bash")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	const n = 5000
	cands := make([]string, n)
	for i := range cands {
		cands[i] = fmt.Sprintf("pppppppppppppppppppp-item %d", i)
	}
	driver := `source "$1"
COMP_LINE="my-prog --v p" COMP_WORDS=(my-prog --v p) COMP_CWORD=2 COMP_TYPE=9 COMPREPLY=()
COMP_POINT=${#COMP_LINE}
fn=$(complete -p my-prog | sed -n 's/.* -F \([^ ]*\) .*/\1/p')
"$fn"
printf '%s\n' "${#COMPREPLY[@]}" "${COMPREPLY[0]}" "${COMPREPLY[n-1]}"
`
	driverPath := filepath.Join(dir, "many.sh")
	if err := os.WriteFile(driverPath, []byte(strings.Replace(driver, "n-1", strconv.Itoa(n-1), 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cands.txt"), []byte(strings.Join(cands, "\n")+"\n:rotini:none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, driverPath, scriptPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "CANDS=cands.txt", "ARGS_OUT=args.txt", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := fmt.Sprintf("%d\npppppppppppppppppppp-item\\ 0\npppppppppppppppppppp-item\\ %d\n", n, n-1)
	if string(out) != want {
		t.Fatalf("completing %d candidates printed\n%s\nwant\n%s", n, out, want)
	}
	// About 0.4 s of CPU on bash 3.2 when each candidate costs the same; 3.5 s or more when
	// the cost grows with the number of candidates.
	if cpu := cmd.ProcessState.UserTime() + cmd.ProcessState.SystemTime(); cpu > 2*time.Second {
		t.Errorf("completing %d candidates took %v of CPU, want under 2s", n, cpu)
	}
}

// TestCompletionScript_pwshInsertsLiteralWords completes hostile candidates with the PowerShell
// script through TabExpansion2, the function PowerShell's TAB calls, then runs each completed
// line: the program must receive the candidate as one argument, PowerShell's typographic quotes,
// dashes and --% included.
func TestCompletionScript_pwshInsertsLiteralWords(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the fake program is a shell script")
	}
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh is not installed")
	}
	script, err := completionScript("my-prog", "powershell", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	dir := completionQuotingDir(t)
	scriptPath := filepath.Join(dir, "completion.ps1")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	// Prints INSERT and the inserted text for each candidate, then runs the completed line, which
	// prints an ARG line for each argument the program receives.
	driver := `param($Script, $Line)
. $Script
$c = TabExpansion2 -inputScript $Line -cursorColumn $Line.Length
foreach ($m in $c.CompletionMatches) {
    "INSERT " + $m.CompletionText
    $run = $Line.Substring(0, $c.ReplacementIndex) + $m.CompletionText + $Line.Substring($c.ReplacementIndex + $c.ReplacementLength)
    try { Invoke-Expression $run } catch { "FAILED $_" }
}
`
	driverPath := filepath.Join(dir, "driver.ps1")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	cands := append(slices.Clone(hostileCandidates), "x’;touch pwned;’", "”;touch pwned;“", "a’b",
		"–a.b", "—a.b", "―a.b", "-a:", "-a:b", "–a:", "--%x")
	for _, line := range []string{"my-prog --v ", "my-prog --v '"} {
		run := runQuotingDriver(t, dir, cands, pwsh, "-NoProfile", "-NonInteractive", "-File", driverPath, scriptPath, line)
		var inserted []string
		var got [][]string
		for out := range strings.Lines(run.out) {
			out = strings.TrimRight(out, "\r\n")
			switch {
			case strings.HasPrefix(out, "INSERT "):
				inserted = append(inserted, strings.TrimPrefix(out, "INSERT "))
				got = append(got, nil)
			case strings.HasPrefix(out, "ARG ") && len(got) > 0:
				got[len(got)-1] = append(got[len(got)-1], strings.TrimPrefix(out, "ARG "))
			default:
				t.Errorf("%q: unexpected output %q", line, out)
			}
		}
		if len(got) != len(cands) {
			t.Fatalf("%q: %d candidates completed, want %d\n%s", line, len(got), len(cands), run.out)
		}
		for i, c := range cands {
			if want := []string{"--v", c}; !slices.Equal(got[i], want) {
				t.Errorf("%q: inserting %q, the program received %q, want %q", line, inserted[i], got[i], want)
			}
		}
	}
	// A --% earlier on the line reaches __complete as the word itself.
	for _, line := range []string{"my-prog @('--%') ", "my-prog '--%' "} {
		run := runQuotingDriver(t, dir, []string{"a"}, pwsh, "-NoProfile", "-NonInteractive", "-File", driverPath, scriptPath, line)
		if want := []string{"--%", ""}; !slices.Equal(run.sent, want) {
			t.Errorf("%q: the program got %q to complete, want %q", line, run.sent, want)
		}
	}
}

// TestCompletionScript_fishInsertsLiteralWordsOnce completes hostile candidates with the fish
// script, asks fish for each value as it inserts it (complete --escape), and evaluates that text:
// it must be one argument holding the candidate. The program must run once per completion.
// fish leaves a leading ~ unescaped in every completion, so no candidate here starts with one.
func TestCompletionScript_fishInsertsLiteralWordsOnce(t *testing.T) {
	t.Parallel()
	fish, err := exec.LookPath("fish")
	if err != nil {
		t.Skip("fish is not installed")
	}
	script, err := completionScript("my-prog", "fish", completionEnvs{})
	if err != nil {
		t.Fatal(err)
	}
	dir := completionQuotingDir(t)
	scriptPath := filepath.Join(dir, "completion.fish")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	// Prints, for each inserted text, the arguments fish reads from it, one ARG line each.
	driver := `source $argv[1]
for c in (complete --escape -C $argv[2])
    set -l v (string split -m 1 \t -- $c)[1]
    echo INSERT
    eval "printf 'ARG %s\n' $v"
end
`
	driverPath := filepath.Join(dir, "driver.fish")
	if err := os.WriteFile(driverPath, []byte(driver), 0o600); err != nil {
		t.Fatal(err)
	}
	var cands []string
	for _, c := range hostileCandidates {
		if !strings.HasPrefix(c, "~") {
			cands = append(cands, c)
		}
	}
	for _, tc := range []struct{ line, sent string }{
		{"my-prog --v ", ""},
		{`my-prog --v 'it`, "it"},
		{`my-prog --v it\'`, "it'"},
	} {
		want := cands
		if tc.sent != "" {
			want = []string{"it's", "it's me"}
		}
		run := runQuotingDriver(t, dir, want, fish, "--no-config", driverPath, scriptPath, tc.line)
		if got := run.sent[len(run.sent)-1]; got != tc.sent {
			t.Errorf("%q: the program got the current word %q, want %q", tc.line, got, tc.sent)
		}
		var got [][]string
		for out := range strings.Lines(run.out) {
			out = strings.TrimSuffix(out, "\n")
			switch {
			case out == "INSERT":
				got = append(got, nil)
			case strings.HasPrefix(out, "ARG ") && len(got) > 0:
				got[len(got)-1] = append(got[len(got)-1], strings.TrimPrefix(out, "ARG "))
			}
		}
		var flat []string
		for _, args := range got {
			if len(args) != 1 {
				t.Errorf("%q: an inserted value reads as the arguments %q", tc.line, args)
				continue
			}
			flat = append(flat, args[0])
		}
		slices.Sort(flat)
		sorted := slices.Sorted(slices.Values(want))
		if !slices.Equal(flat, sorted) {
			t.Errorf("%q: the inserted values read as\n%q\nwant\n%q", tc.line, flat, sorted)
		}
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(calls), "\n"); n != 3 {
		t.Errorf("the program ran %d times for 3 completions, want 3", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Error("a candidate ran a command")
	}
}

// TestCompletionScript_namesDontCollide pins that two programs whose names differ only in "-"
// against "_" get bash and fish scripts with no function or variable name in common, so both
// can be installed side by side.
func TestCompletionScript_namesDontCollide(t *testing.T) {
	t.Parallel()
	names := regexp.MustCompile(`(?m)(?:^|\s)(_{1,2}[A-Za-z0-9_]+)`)
	for _, shell := range []string{"bash", "fish"} {
		seen := map[string]string{}
		for _, prog := range []string{"my-app", "my_app", "my_2dapp"} {
			script, err := completionScript(prog, shell, completionEnvs{})
			if err != nil {
				t.Fatal(err)
			}
			mine := map[string]bool{}
			for _, m := range names.FindAllStringSubmatch(script, -1) {
				if strings.Contains(m[1], "my") {
					mine[m[1]] = true
				}
			}
			if len(mine) == 0 {
				t.Fatalf("%s %s: no names found", shell, prog)
			}
			for n := range mine {
				if other, ok := seen[n]; ok {
					t.Errorf("%s: %s and %s both define %s", shell, other, prog, n)
				}
				seen[n] = prog
			}
		}
	}
}
