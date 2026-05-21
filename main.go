//go:generate go tool gentypes -package models -root Spec -o internal/models/spec.go schemas/spec.json
//go:generate go tool gentypes -package models -root Conf -o internal/models/conf.go schemas/conf.json
//go:generate go run . generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml
package main

func main() {
}
