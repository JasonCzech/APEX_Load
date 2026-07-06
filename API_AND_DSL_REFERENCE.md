# API and DSL Reference

Status: Compatibility landing page

This file used to contain a hand-maintained REST endpoint catalog and DSL
reference. That made it drift from the controller route catalog and the DSL
action catalog. The generated docs below are now the source of truth.

## Current API References

- Generated OpenAPI: [docs/swagger.yaml](docs/swagger.yaml)
- Generated endpoint catalog: [docs/api-endpoint-catalog.json](docs/api-endpoint-catalog.json)
- Runtime routes exposed by the controller:
  - `GET /api/v1/docs/swagger.yaml`
  - `GET /api/v1/docs/endpoint-catalog`
- Workflow guide for automation and operators: [docs/API_USER_GUIDE.md](docs/API_USER_GUIDE.md)

The OpenAPI and endpoint catalog are generated from the controller routing layer
by `cmd/swaggergen` through `go generate ./pkg/api`. Do not hand-edit the
generated files.

## Current DSL References

- Generated action reference: [docs/DSL_ACTION_REFERENCE.md](docs/DSL_ACTION_REFERENCE.md)
- Generated JSON schema: [docs/dsl.schema.json](docs/dsl.schema.json)
- Script lifecycle overview: [docs/SCRIPT_LIFECYCLE.md](docs/SCRIPT_LIFECYCLE.md)
- Quick examples and template variables: [QUICK_REFERENCE.md](QUICK_REFERENCE.md)

The DSL schema and action reference are generated from `pkg/dsl/catalog*.go` by
`go generate ./pkg/dsl`. The catalog also drives validation and dashboard editor
metadata, so generated output should be regenerated after catalog changes.

## Keeping References Current

Run these checks after route or DSL catalog changes:

```bash
go generate ./pkg/api
go generate ./pkg/dsl
go test ./pkg/api ./pkg/dsl
python3 scripts/check-docs.py
```
