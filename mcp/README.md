# FinTrak MCP Server

`fintrak-mcp` exposes the FinTrak ledger to model clients over the Model Context
Protocol using stdio. It is a read-only REST client; it adds no backend route
or schema.

## Build and configure

```bash
go build -o fintrak-mcp .
```

An MCP client configuration can launch the binary with environment variables:

```json
{
  "mcpServers": {
    "fintrak": {
      "command": "fintrak-mcp",
      "env": {
        "FINTRAK_API_URL": "http://localhost:8080/api/v1",
        "FINTRAK_EMAIL": "you@example.com",
        "FINTRAK_PASSWORD": "..."
      }
    }
  }
}
```

`FINTRAK_API_URL`, `FINTRAK_EMAIL`, and `FINTRAK_PASSWORD` configure a normal
login. A token pair may instead be supplied with `FINTRAK_ACCESS_TOKEN` and
`FINTRAK_REFRESH_TOKEN`; both are required because a lone access token cannot
be renewed. Command-line `-email`, `-password`, `-api`, `-log-file`, and
`-log-level` flags exist, but environment configuration is preferred because
command-line secrets are visible to other processes.

## Read-only surface

The registry contains tools for the ledger, reference data, aggregates, and the
API's no-write preview endpoints. The read-only transport guard permits only
the declared tool routes and the session routes needed for login/refresh; it
refuses writes even if a future tool accidentally calls a write method.

The only admitted non-GET operations are the documented previews:

- `POST /transactions/validate`
- `POST /rules/preview`

A few GET routes materialize derived billing cycles or re-seal a legacy
Paperless token. Tools for those routes declare the side effect in their
description and their read-only hint is derived from that declaration. The
`SideEffectingGETs` inventory and the tool registry are audited against the
OpenAPI document.

Authentication is lazy: the first `tools/call` signs in, while `tools/list`
still works with bad credentials. A failed sign-in is returned as a tool error
so the model can report it. stdout is reserved for MCP; logs go to stderr or
`-log-file`. Unpaged list results are bounded to protect model context and
report truncation.

## Development

```bash
go test ./...
go vet ./...
go build ./...
```

The tool audit checks route existence and arguments against
`../backend/openapi.yaml`, and the binary test drives the real stdio protocol.
