package rotini

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

// conptyReportEnv names the file TestTerminal_pseudoConsoleChild writes its findings to; it is
// set only in the child TestTerminal_pseudoConsole starts.
const conptyReportEnv = "ROTINI_TEST_CONPTY_REPORT"

var (
	conptyKernel32                        = syscall.NewLazyDLL("kernel32.dll")
	procCreatePseudoConsole               = conptyKernel32.NewProc("CreatePseudoConsole")
	procClosePseudoConsole                = conptyKernel32.NewProc("ClosePseudoConsole")
	procInitializeProcThreadAttributeList = conptyKernel32.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute         = conptyKernel32.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList     = conptyKernel32.NewProc("DeleteProcThreadAttributeList")
)

// TestTerminal_pseudoConsole runs TestTerminal_pseudoConsoleChild in a copy of the test binary
// attached to a new pseudo console, so the console paths run where no real console exists.
func TestTerminal_pseudoConsole(t *testing.T) {
	t.Parallel()
	if os.Getenv(conptyReportEnv) != "" {
		t.Skip("the pseudo console's child runs only TestTerminal_pseudoConsoleChild")
	}
	if err := procCreatePseudoConsole.Find(); err != nil {
		t.Skipf("this Windows has no pseudo console: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "report")
	args := []string{"-test.run=^TestTerminal_pseudoConsoleChild$", "-test.count=1"}
	code, output := runInPseudoConsole(t, exe, args, append(os.Environ(), conptyReportEnv+"="+report))
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("the child (exit %d) wrote no report: %v\nconsole output:\n%s", code, err, output)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if lines[len(lines)-1] != "done" {
		t.Fatalf("the child (exit %d) stopped early: %q\nconsole output:\n%s", code, data, output)
	}
	for _, line := range lines[:len(lines)-1] {
		t.Error(line)
	}
	if code != 0 && len(lines) == 1 {
		t.Errorf("the child exited %d\nconsole output:\n%s", code, output)
	}
}

// TestTerminal_pseudoConsoleChild checks the terminal paths against the pseudo console its
// parent attached it to, and writes each failure, then "done", to the report file.
func TestTerminal_pseudoConsoleChild(t *testing.T) {
	report := os.Getenv(conptyReportEnv)
	if report == "" {
		t.Skip("runs as TestTerminal_pseudoConsole's child, attached to a pseudo console")
	}
	var failures []string
	fail := func(format string, args ...any) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}
	inputs := []namedFile{{"stdin", os.Stdin}}
	outputs := []namedFile{{"stdout", os.Stdout}}
	if f, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err != nil {
		fail("open CONIN$: %v", err)
	} else {
		defer f.Close()
		inputs = append(inputs, namedFile{"CONIN$", f})
	}
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err != nil {
		fail("open CONOUT$: %v", err)
	} else {
		defer f.Close()
		outputs = append(outputs, namedFile{"CONOUT$", f})
	}
	for _, s := range slices.Concat(inputs, outputs) {
		if s.f == nil || !IsTerminal(s.f) {
			fail("IsTerminal(%s) = false, want true", s.name)
		}
	}
	for _, in := range inputs {
		if in.f == nil {
			continue
		}
		_, err := OpenInput(newContext().WithStdin(in.f), "-")
		if err == nil || CategoryOf(err) != CategoryUsage || !strings.Contains(err.Error(), "stdin is a terminal") {
			fail(`OpenInput with %s and "-" = %v, want the "stdin is a terminal" usage error`, in.name, err)
		}
		read := make(chan string, 1)
		go func() {
			got, err := stRTX(in.f).slurpStdin()
			read <- fmt.Sprintf("%q, %v", got, err)
		}()
		select {
		case got := <-read:
			if got != `"", <nil>` {
				fail("the stdin channel over %s read %s, want empty", in.name, got)
			}
		case <-time.After(10 * time.Second):
			fail("the stdin channel over %s blocked; a terminal reads as empty", in.name)
		}
	}
	for _, out := range outputs {
		if out.f == nil {
			continue
		}
		p, _ := bufferedProgram(func(rtx *Context) {
			if rtx.Stdout != out.f {
				fail("buffered output over %s gave the handler %T, want the terminal itself", out.name, rtx.Stdout)
			}
		}, out.f)
		p.Run(nil)
	}
	for _, f := range failures {
		t.Error(f)
	}
	if err := os.WriteFile(report, []byte(strings.Join(append(failures, "done"), "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type namedFile struct {
	name string
	f    *os.File
}

// runInPseudoConsole runs exe with args and env attached to a new 80x25 pseudo console, and
// returns its exit code and what it wrote to the console (as the console renders it).
func runInPseudoConsole(t *testing.T, exe string, args, env []string) (code uint32, output string) {
	t.Helper()
	var inRead, inWrite, outRead, outWrite syscall.Handle
	if err := syscall.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		t.Fatalf("input pipe: %v", err)
	}
	defer syscall.CloseHandle(inWrite)
	if err := syscall.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		syscall.CloseHandle(inRead)
		t.Fatalf("output pipe: %v", err)
	}
	out := os.NewFile(uintptr(outRead), "pseudo console output")

	var console syscall.Handle
	const size = 25<<16 | 80 // COORD{X: 80, Y: 25}, passed by value
	hr, _, _ := procCreatePseudoConsole.Call(size, uintptr(inRead), uintptr(outWrite), 0, uintptr(unsafe.Pointer(&console)))
	// The pseudo console keeps its own copies of the ends it was given.
	syscall.CloseHandle(inRead)
	syscall.CloseHandle(outWrite)
	if int32(hr) < 0 {
		out.Close()
		t.Fatalf("CreatePseudoConsole: HRESULT %#x", uint32(hr))
	}

	// The console writes as the child does; read it all the while, or a full pipe stalls both.
	var drained bytes.Buffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(&drained, out)
	}()
	closeConsole := sync.OnceValue(func() bool {
		procClosePseudoConsole.Call(uintptr(console))
		select {
		case <-done:
			out.Close()
			return true
		case <-time.After(30 * time.Second):
			return false // the reader still holds out; leave it rather than race it
		}
	})
	defer closeConsole()

	var listSize uintptr // a first call, which fails, reports the size the list needs
	procInitializeProcThreadAttributeList.Call(0, 1, 0, uintptr(unsafe.Pointer(&listSize)))
	if listSize == 0 {
		t.Fatal("InitializeProcThreadAttributeList gave no size")
	}
	word := unsafe.Sizeof(uintptr(0))
	list := make([]uintptr, (listSize+word-1)/word)
	if ok, _, err := procInitializeProcThreadAttributeList.Call(uintptr(unsafe.Pointer(&list[0])), 1, 0, uintptr(unsafe.Pointer(&listSize))); ok == 0 {
		t.Fatalf("InitializeProcThreadAttributeList: %v", err)
	}
	defer procDeleteProcThreadAttributeList.Call(uintptr(unsafe.Pointer(&list[0])))
	const procThreadAttributePseudoConsole = 0x00020016
	// The attribute's value is the console handle itself, not a pointer to it.
	if ok, _, err := procUpdateProcThreadAttribute.Call(uintptr(unsafe.Pointer(&list[0])), 0,
		procThreadAttributePseudoConsole, uintptr(console), unsafe.Sizeof(console), 0, 0); ok == 0 {
		t.Fatalf("UpdateProcThreadAttribute: %v", err)
	}

	// STARTUPINFOEXW. Null standard handles under STARTF_USESTDHANDLES keep the child from
	// inheriting this process's (often redirected) ones, so it gets the pseudo console's.
	var si struct {
		syscall.StartupInfo
		attributeList *uintptr
	}
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.attributeList = &list[0]

	words := []string{syscall.EscapeArg(exe)}
	for _, a := range args {
		words = append(words, syscall.EscapeArg(a))
	}
	cmdline, err := syscall.UTF16PtrFromString(strings.Join(words, " "))
	if err != nil {
		t.Fatal(err)
	}
	var block []uint16
	for _, kv := range env {
		block = append(block, utf16.Encode([]rune(kv))...)
		block = append(block, 0)
	}
	block = append(block, 0) // the block ends with an empty string

	const extendedStartupInfoPresent = 0x00080000
	var pi syscall.ProcessInformation
	err = syscall.CreateProcess(nil, cmdline, nil, nil, false,
		extendedStartupInfoPresent|syscall.CREATE_UNICODE_ENVIRONMENT, &block[0], nil, &si.StartupInfo, &pi)
	runtime.KeepAlive(list)
	runtime.KeepAlive(&si)
	if err != nil {
		t.Fatalf("CreateProcess: %v", err)
	}
	defer syscall.CloseHandle(pi.Process)
	syscall.CloseHandle(pi.Thread)

	if ev, err := syscall.WaitForSingleObject(pi.Process, uint32((2 * time.Minute).Milliseconds())); ev != syscall.WAIT_OBJECT_0 {
		syscall.TerminateProcess(pi.Process, 1)
		syscall.WaitForSingleObject(pi.Process, 10000)
		t.Fatalf("the child did not finish (wait %#x): %v", ev, err)
	}
	if err := syscall.GetExitCodeProcess(pi.Process, &code); err != nil {
		t.Fatalf("GetExitCodeProcess: %v", err)
	}
	if !closeConsole() {
		t.Error("the pseudo console's output never closed")
		return code, ""
	}
	return code, drained.String()
}
