//go:generate go tool gentypes -package internal -root Spec -o internal/spec_types.go schemas/spec.json
//go:generate go tool gentypes -package internal -root Conf -o internal/conf_types.go schemas/conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

func main() {
}
