#!/usr/bin/env bash
# Serves this checkout as github.com/go-rotini/rotini@v1.0.0 from a throwaway file-based module
# proxy, so the demo in rotini.tape can run the real `go get ...@latest` commands before the
# module is published. Prints the environment to export; everything lives in one temp dir.
#
# The module cache is a temp dir too: a fake v1.0.0 must never land in the real cache, where it
# would clash with the published one. Other dependencies come from the real cache when present,
# else from proxy.golang.org.
set -euo pipefail

src=$(cd "$(dirname "$0")/../.." && pwd)
version=v1.0.0
work=$(mktemp -d)
proxy="$work/proxy/github.com/go-rotini/rotini/@v"
stage="$work/stage/github.com/go-rotini/rotini@$version"
mkdir -p "$proxy" "$stage"

# The module's files: tracked plus untracked-but-not-ignored, minus the docs site (its own module).
cd "$src"
git ls-files -co --exclude-standard | grep -v '^docs/' | while IFS= read -r f; do
  [ -f "$f" ] || continue
  mkdir -p "$stage/$(dirname "$f")"
  cp "$f" "$stage/$f"
done

(cd "$work/stage" && zip -qrD "$proxy/$version.zip" "github.com")
cp go.mod "$proxy/$version.mod"
printf '{"Version":"%s","Time":"2026-01-01T00:00:00Z"}\n' "$version" >"$proxy/$version.info"
echo "$version" >"$proxy/list"

cat <<EOF
export GOPROXY=file://$work/proxy,file://$(go env GOMODCACHE)/cache/download,https://proxy.golang.org
export GONOSUMDB=github.com/go-rotini/rotini
export GOMODCACHE=$work/modcache
export GOFLAGS=-modcacherw
export ROTINI_DEMO_WORK=$work
EOF
