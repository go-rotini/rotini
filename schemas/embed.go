package schemas

import _ "embed"

//go:embed spec.json
var RotiniSchemaSpec []byte

//go:embed conf.json
var RotiniSchemaConf []byte
