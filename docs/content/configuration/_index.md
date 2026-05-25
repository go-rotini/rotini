---
title: "configuration"
---

# Configuration File

## Schema

{{< code title="schema-conf.json" language="json" open="true" collapsible="true" copy="false" >}}

{{< /code >}}

## Examples

### YAML

{{< code title="yaml" language="yaml" open="true" collapsible="true" copy="true" >}}
initialize:
  package: "internal"
validate:
  behavior: "fail_collect"
generate:
  cmd:
    package: "internal/cmd"
    gen_file: "handlers.gen.go"
    prune:
      enabled: true
      keep: []
  framework:
    package: "internal/rotini"
    gen_file: "rotini.gen.go"
    additional_imports: []
{{< /code >}}

### JSON

{{< code title="json" language="json" open="true" collapsible="true" copy="true" >}}

{{< /code >}}
