# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly by emailing **matthewcgetz@gmail.com**. Do not open a public issue.

You should receive a response within 72 hours. If accepted, a fix will be developed privately and released as a patch version.

## Threat Model

`fs` is a filesystem-helpers library. Its security posture is shaped around three classes of risk:

1. **Untrusted input as a path.** A user-controlled filename, an archive entry's name, or a glob pattern reaching a privileged context.
2. **Untrusted input as bytes.** Reading a file that may be attacker-controlled (or pointed at `/dev/zero`); extracting an archive from an untrusted source.
3. **Concurrent / racing actors.** TOCTOU windows between a check (`Stat`) and a use (`Open`), or between resolving a path and acting on it.

The package's job is to make the safe path the easy path. The defenses below are the ones it bakes in by default.

## Resource Limits

Every entry point that consumes external bytes is bounded by default. Disabling the bound is opt-in and documented.

- **Bounded reads.** `ReadFile`, `ReadLines`, `ReadFirstLine`, and `OpenLines` honor `WithMaxSize` (default `DefaultMaxReadSize` = 100 MiB). A file exceeding the cap returns `ErrFileTooLarge`. The cap defends against `/dev/zero`-class reads, runaway logs, and similarly pathological inputs. Set the cap to `0` only when the source is a trusted regular file of known size.
- **Bounded archive extraction.** `ExtractArchive` / `ExtractArchiveFile` honor `WithArchiveMaxBytes` (default 10 GiB). The cumulative extracted byte count is tracked across every entry; exceeding it returns `ErrArchiveTooLarge`. Defends against zip-bomb / tar-bomb attacks. Zip stream extraction additionally caps the temp-file buffer at `maxBytes+1` so a crafted compressed payload cannot exhaust disk before the cap trips.
- **Bounded find-up walks.** `FindUp`, `FindUpAll`, and `ProjectRoot` honor `WithMaxAncestors` (default 32). Defends against pathological symlink loops and deeply-nested mount points that would otherwise drive the walk indefinitely.

## Path-Traversal Defense

- **Zip-slip / tar-slip.** Every archive entry resolves through `MustBeChildOf(dst, ...)` before any filesystem write. A crafted entry named `../../../etc/passwd` (or an absolute path) errors with `ErrEscapesRoot` rather than writing outside the extraction root. Hand-rolled extraction using `archive/zip` or `archive/tar` directly is the classic vulnerability; always use `ExtractArchive`.
- **Symlink-escape inside archives.** Tar symlink entries whose target resolves outside `dst` are refused with `ErrEscapesRoot` at extraction time (see `archive_extract_tar.go:validateTarSymlinkTarget`).
- **Mode masking.** Archive entries are masked to `0o644` for files and `0o755` for directories by default. Setuid, setgid, sticky, and other mode bits in the archive are stripped unless `WithPreserveMode(true)` is set explicitly. Don't enable mode preservation for archives from untrusted sources.
- **`MustBeChildOf` / `IsSubpath`.** Public predicates for callers writing their own confined-path logic. Both perform `filepath.Abs` + `filepath.Clean` before comparison.
- **`EvalSymlinksWithin`.** Resolves all symlinks in a path while verifying the resolved target stays inside a parent root. Use when a caller-supplied path must be a real on-disk location AND must not escape its sandbox.

## TOCTOU Resistance

Many filesystem APIs have time-of-check-to-time-of-use windows. The package provides primitives that close those windows where the platform allows.

- **`OpenNoFollow`.** Opens a path with POSIX `O_NOFOLLOW`. If the final component is a symlink, returns `ErrSymlinkLoop`. Defends against link-replace attacks where an attacker swaps the target between a `Stat` and an `Open`. Intermediate components are still resolved normally; only the final component is protected.
- **`OpenAt`.** Resolves a relative path through a held directory file descriptor via POSIX `openat(2)`. Defends against directory-replace races where a directory is swapped for a symlink mid-walk.
- **Known limitation on Windows.** `OpenAt` falls back to `filepath.Join` + `os.OpenFile`; the fallback is **not** race-safe and is documented as such. Callers needing TOCTOU resistance on Windows must use other hardening (transactional NTFS, locked parent directories, etc.). `OpenNoFollow` opens the reparse point itself rather than following it; callers needing strict "refuse if final component is a symlink" semantics on Windows should inspect the returned file's mode.
- **The `Exists` asymmetry.** `Exists(path)` returns `false` on permission errors as well as missing paths. This is deliberate for ergonomics but it means `if !Exists(p) { create(p) }` is unsafe in privileged contexts; a privilege-restricted attacker can hide an existing file. Use `Stat` directly and inspect the error when correctness depends on the distinction.

## Filename Sanitization

- **`SanitizeFilename`** strips ASCII control bytes, the Windows-illegal characters (`< > : " | ? * / \`), and trailing dots and spaces. When the cleaned stem matches a Windows reserved device name (CON, PRN, AUX, NUL, COM1–COM9, LPT1–LPT9), an underscore is inserted **before** the extension; `CON.txt` becomes `CON_.txt`. Suffixing the whole filename (`CON.txt_`) would leave Windows still treating the file as the CON device.
- **`IsReservedName`** is portability-safe: it returns `true` for reserved names regardless of the host OS so callers writing files for cross-platform consumption (archive extraction, scaffolding) catch them before they hit a Windows reader.

## Hash and Integrity Operations

- **`HashCompare`** uses `crypto/subtle.ConstantTimeCompare` so integrity check call sites are not timing-oracles.
- **MD5 and SHA-1 are exposed** for non-security uses (legacy compatibility, content-addressed caches, file-integrity checks against published digests). They are documented as broken for security purposes; do not use them for anything an attacker can influence.
- **Default algorithm.** `HashAlgo`'s zero value is `HashSHA256`. Pick SHA-256 or SHA-512 for new code unless you have a specific legacy reason.

## What the Package Does NOT Do

- **No shell execution.** The package never invokes a shell or `exec`s a subprocess. Glob patterns are evaluated via `path/filepath.Match`, not `sh -c`.
- **No silent env mutation.** The package does not call `os.Setenv` or otherwise mutate process-global env state. (`Expand` reads env vars via `os.LookupEnv` but never writes.)
- **No automatic privilege escalation.** Symlink helpers on Windows surface the privilege error clearly when the calling user lacks the `SeCreateSymbolicLinkPrivilege`; the package does not attempt to invoke `runas` or otherwise elevate.
- **No telemetry.** The package emits no logs of its own from library entry points. `Watcher` accepts a `*slog.Logger` via `WithLogger`; the default is a discard logger.

## Known Caveats

- **`Symlink`'s idempotency is best-effort under concurrent callers.** Between `os.Readlink` and `os.Symlink` another process can create the link; POSIX `symlink(2)` is atomic for create-if-not-exists but Go's stdlib does not expose the flags needed to thread that atomicity through.
- **`ProjectRoot` caches results process-globally with no invalidation.** Long-lived processes that change project layouts on disk will see stale answers. Designed for CLI tools; consider it best-effort in daemons.
- **The watcher's polling backend reads mtimes from `os.Lstat`.** On filesystems that round mtime to second resolution (FAT, some SMB mounts), back-to-back writes within one second can be missed. Use `WithPolling`-with-a-finer-interval where this matters; the platform-native backend (post-v0.1) will close this gap on supported filesystems.

For the full list of caveats including TOCTOU, the `Exists` permission asymmetry, watcher debounce latency, and macOS `/var` -> `/private/var` resolution, see the "Pitfalls" section of the package documentation in `doc.go`.
