package rotini

import _ "embed"

//go:embed schema-spec.json
var SchemaSpec []byte

//go:embed schema-conf.json
var SchemaConf []byte
