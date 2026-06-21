package rotini

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFakeBinaryPath writes an executable script on PATH and returns its full path (so a
// test can hash it). The CI is unix.
func writeFakeBinaryPath(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

// The hidden __rotini entry prints a self-identifying version report.
func TestRotiniVersionEntry(t *testing.T) {
	def := Definition{Name: "app", Handler: "App"}
	p, out, _ := remoteProgram(def, []string{rotiniVersionCommand})
	if code, _ := p.run(p.args); code != 0 {
		t.Fatalf("__rotini exit = %d, want 0", code)
	}
	if got := strings.TrimSpace(out.String()); !strings.HasPrefix(got, "rotini") {
		t.Errorf("__rotini output = %q, want a 'rotini …' report", got)
	}
}

func TestParseRotiniVersionReport(t *testing.T) {
	cases := map[string]string{
		"rotini v1.2.3\n":          "v1.2.3",
		"rotini v0.0.0-dev\nnoise": "v0.0.0-dev",
		"rotini \n":                "",
		"not a report":             "",
		"":                         "",
	}
	for in, want := range cases {
		if got := parseRotiniVersionReport(in); got != want {
			t.Errorf("parseRotiniVersionReport(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSHA256Matches(t *testing.T) {
	const hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if !sha256Matches("sha256:"+hex, hex) {
		t.Error("prefixed pin should match")
	}
	if !sha256Matches(strings.ToUpper(hex), hex) {
		t.Error("case-insensitive pin should match")
	}
	if sha256Matches(hex, "ffff") {
		t.Error("different digest must not match")
	}
}

// A pinned sha256 that matches lets dispatch proceed; a mismatch aborts with a typed
// RemoteVerificationFailed before the binary runs.
func TestRemoteVerify_sha256(t *testing.T) {
	path := writeFakeBinaryPath(t, "app-ext", "#!/bin/sh\necho ran\nexit 0\n")
	sum, err := fileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("match proceeds", func(t *testing.T) {
		def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
			{Name: "ext", Binary: "app-ext", Verify: &RemoteVerify{SHA256: "sha256:" + sum}},
		}}
		p, out, errb := remoteProgram(def, []string{"ext"})
		if code, _ := p.run(p.args); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb)
		}
		if strings.TrimSpace(out.String()) != "ran" {
			t.Errorf("output = %q, want 'ran'", out)
		}
	})

	t.Run("mismatch aborts", func(t *testing.T) {
		bad := "1111111111111111111111111111111111111111111111111111111111111111"
		def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
			{Name: "ext", Binary: "app-ext", Verify: &RemoteVerify{SHA256: bad}},
		}}
		p, out, _ := remoteProgram(def, []string{"ext"})
		code, err := p.run(p.args)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if strings.Contains(out.String(), "ran") {
			t.Error("binary must NOT run on a hash mismatch")
		}
		var re *RemoteError
		if !errors.As(err, &re) || re.Kind != RemoteVerificationFailed {
			t.Fatalf("err = %v, want *RemoteError verification-failed", err)
		}
		if CategoryOf(err) != CategoryInternal {
			t.Errorf("category = %v, want internal", CategoryOf(err))
		}
	})
}

// The version handshake aborts on a cross-major mismatch and proceeds on same-major.
func TestRemoteVerify_versionHandshake(t *testing.T) {
	orig := hostRotiniVersion
	hostRotiniVersion = func() string { return "v1.5.0" }
	t.Cleanup(func() { hostRotiniVersion = orig })

	probe := func(remoteVer string) string {
		return "#!/bin/sh\nif [ \"$1\" = \"" + rotiniVersionCommand + "\" ]; then echo \"rotini " + remoteVer + "\"; exit 0; fi\necho ran\n"
	}

	t.Run("cross-major aborts", func(t *testing.T) {
		writeFakeBinaryPath(t, "app-ext", probe("v2.0.0"))
		def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
			{Name: "ext", Binary: "app-ext", Verify: &RemoteVerify{Version: true}},
		}}
		p, out, _ := remoteProgram(def, []string{"ext"})
		code, err := p.run(p.args)
		if code != 1 {
			t.Errorf("exit = %d, want 1", code)
		}
		if strings.Contains(out.String(), "ran") {
			t.Error("binary must NOT run on a version mismatch")
		}
		var ve *CompositionVersionError
		if !errors.As(err, &ve) || ve.Arm != CompositionBinaryArm {
			t.Fatalf("err = %v, want a binary-arm *CompositionVersionError", err)
		}
		if ve.Want != "v1.5.0" || ve.Got != "v2.0.0" {
			t.Errorf("Want/Got = %q/%q", ve.Want, ve.Got)
		}
	})

	t.Run("same-major proceeds", func(t *testing.T) {
		writeFakeBinaryPath(t, "app-ext", probe("v1.9.9"))
		def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
			{Name: "ext", Binary: "app-ext", Verify: &RemoteVerify{Version: true}},
		}}
		p, out, errb := remoteProgram(def, []string{"ext"})
		if code, _ := p.run(p.args); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb)
		}
		if strings.TrimSpace(out.String()) != "ran" {
			t.Errorf("output = %q, want 'ran'", out)
		}
	})

	t.Run("unparseable remote skips (proceeds)", func(t *testing.T) {
		writeFakeBinaryPath(t, "app-ext", "#!/bin/sh\necho ran\n") // ignores __rotini
		def := Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
			{Name: "ext", Binary: "app-ext", Verify: &RemoteVerify{Version: true}},
		}}
		p, out, errb := remoteProgram(def, []string{"ext"})
		if code, _ := p.run(p.args); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb)
		}
		if strings.TrimSpace(out.String()) != "ran" {
			t.Errorf("output = %q, want 'ran' (best-effort skip)", out)
		}
	})
}

// The keyless signature rung (D-W9.10) verifies a sidecar bundle against an expected
// identity before dispatch, via the wired verifier seam. It fails CLOSED: a valid bundle
// proceeds; a verifier error, a missing bundle, or no wired verifier all abort with a typed
// RemoteVerificationFailed before the binary runs.
func TestRemoteVerify_signature(t *testing.T) {
	const issuer, subject = "https://token.actions.githubusercontent.com", "https://github.com/acme/clis/.github/workflows/release.yml@refs/tags/v1"
	sigDef := func() Definition {
		return Definition{Name: "app", Handler: "App", RemoteCommands: []RemoteDef{
			{Name: "ext", Binary: "app-ext", Verify: &RemoteVerify{Signature: &RemoteSignatureVerify{Issuer: issuer, Subject: subject}}},
		}}
	}
	// writeBundle drops the sidecar bundle next to the fake binary.
	writeBundle := func(t *testing.T, binPath string) {
		t.Helper()
		if err := os.WriteFile(binPath+keylessBundleSuffix, []byte(`{"fake":"bundle"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	useVerifier := func(t *testing.T, v keylessVerifier) {
		t.Helper()
		orig := verifyKeyless
		verifyKeyless = v
		t.Cleanup(func() { verifyKeyless = orig })
	}

	t.Run("valid bundle proceeds", func(t *testing.T) {
		path := writeFakeBinaryPath(t, "app-ext", "#!/bin/sh\necho ran\nexit 0\n")
		writeBundle(t, path)
		var gotBundle, gotIssuer, gotSubject string
		useVerifier(t, func(bin, bundle, iss, sub string) error {
			gotBundle, gotIssuer, gotSubject = bundle, iss, sub
			return nil
		})
		p, out, errb := remoteProgram(sigDef(), []string{"ext"})
		if code, _ := p.run(p.args); code != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", code, errb)
		}
		if strings.TrimSpace(out.String()) != "ran" {
			t.Errorf("output = %q, want 'ran'", out)
		}
		if gotBundle != path+keylessBundleSuffix || gotIssuer != issuer || gotSubject != subject {
			t.Errorf("verifier got bundle=%q issuer=%q subject=%q", gotBundle, gotIssuer, gotSubject)
		}
	})

	t.Run("verifier rejection aborts", func(t *testing.T) {
		path := writeFakeBinaryPath(t, "app-ext", "#!/bin/sh\necho ran\nexit 0\n")
		writeBundle(t, path)
		useVerifier(t, func(_, _, _, _ string) error { return errors.New("identity mismatch") })
		assertSignatureAbort(t, sigDef(), "identity mismatch")
	})

	t.Run("missing bundle aborts", func(t *testing.T) {
		writeFakeBinaryPath(t, "app-ext", "#!/bin/sh\necho ran\nexit 0\n") // no sidecar
		useVerifier(t, func(_, _, _, _ string) error { return nil })
		assertSignatureAbort(t, sigDef(), keylessBundleSuffix)
	})

	t.Run("no verifier wired fails closed", func(t *testing.T) {
		path := writeFakeBinaryPath(t, "app-ext", "#!/bin/sh\necho ran\nexit 0\n")
		writeBundle(t, path)
		useVerifier(t, nil)
		assertSignatureAbort(t, sigDef(), "no sigstore verifier is wired")
	})
}

// assertSignatureAbort runs the "ext" remote and asserts dispatch aborted before the binary
// ran with a typed RemoteVerificationFailed whose message contains want.
func assertSignatureAbort(t *testing.T, def Definition, want string) {
	t.Helper()
	p, out, _ := remoteProgram(def, []string{"ext"})
	code, err := p.run(p.args)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if strings.Contains(out.String(), "ran") {
		t.Error("binary must NOT run when signature verification fails")
	}
	var re *RemoteError
	if !errors.As(err, &re) || re.Kind != RemoteVerificationFailed {
		t.Fatalf("err = %v, want *RemoteError verification-failed", err)
	}
	if CategoryOf(err) != CategoryInternal {
		t.Errorf("category = %v, want internal", CategoryOf(err))
	}
	if !strings.Contains(re.Error(), want) {
		t.Errorf("error %q does not contain %q", re.Error(), want)
	}
}
