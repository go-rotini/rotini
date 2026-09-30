# rotini

TODO: write a short intro to the rotini cli framework package.

## Quick Start

Requires **Go 1.27** or later.

### 1. Initialize

```bash
mkdir helloworld
cd helloworld
go mod init github.com/me/helloworld

go get -tool github.com/go-rotini/rotini/cmd/rotini@latest
go get github.com/go-rotini/rotini/cmd/rotini@latest
go tool rotini init helloworld
```

### 2. Review the generated spec file and modify

```yaml
$schema: ./.rotini-schema.spec.json
version: 0.0.0
command:
  name: helloworld
  flags:
    - name: help
      summary: print help
      identifiers: [-h, --help]
      schema: { type: bool }
    - name: version
      summary: print version
      identifiers: [-v, --version]
      schema: { type: bool }
  commands:
    - name: help
      summary: print help
      description: Print help for a command.
      arguments:
        - name: command
          summary: the command path to print help for
          schema: { type: '[]string' }
      flags:
        - name: help
          summary: print help
          identifiers: [-h, --help]
          schema: { type: bool }
    - name: version
      summary: print version
      description: Print the helloworld version.
      flags:
        - name: help
          summary: print help
          identifiers: [-h, --help]
          schema: { type: bool }
```

### 3. Review the generated conf file and modify

```yaml
$schema: ./.rotini-schema.conf.json
version: 0.0.0
generate:
  schemas:
    conf:
      file: cmd/helloworld/.rotini-schema.conf.json
    spec:
      file: cmd/helloworld/.rotini-schema.spec.json
  packages:
    - type: main
      file: cmd/helloworld/main.go
    - type: cmd
      file: internal/cmd/helloworld/zz_rotini.go
  features:
    - type: help
      enabled: true
    - type: completion
      enabled: false
    - type: man
      enabled: false
    - type: markdown
      enabled: false
validate:
  fail: collect
```

### 4. Review the generated handler file and modify

```go

```

### 5. Review the generated main file and modify

```go
//go:generate go tool rotini generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

import (
	cmd "clitest/internal/cmd/clitest"
)

// go build -ldflags "-X main.version=1.2.3" ./cmd/...
var version = "0.0.0"

func main() {
	cmd.Program.
		WithVersion(version).
		Execute()
}
```

### 6. Generate, build, and run

```bash
go generate ./...
go build ./cmd/... # go install ./cmd/...

./helloworld --help # helloworld --help
```

## Documentation

For more information, see the [rotini](https://rotini.dev) documentation.

- Full API reference is available on [pkg.go.dev](https://pkg.go.dev/github.com/go-rotini/yaml).
- See [COMPATIBILITY.md](COMPATIBILITY.md) to understand what a version promises and [UPGRADING.md](UPGRADING.md) to understand upgrading versions.
- See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines on how to contribute to this project.
- This project follows a code of conduct to ensure a welcoming community. See [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
- To report a vulnerability, see [SECURITY.md](SECURITY.md).
- This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
