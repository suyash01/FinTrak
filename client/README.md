# FinTrak Shared Go API Client

`client/api` is the hand-written Go REST client used by the terminal client and
MCP server. It is a pure client: it adds no backend routes, database code, or
server-side session policy.

## Authentication and session replay

The browser uses httpOnly cookies, which a non-browser client cannot rely on a
standard `http.CookieJar` to replay. This client therefore parses `Set-Cookie`
responses itself and keeps both session cookies in memory:

- `fintrak_token` is attached to ordinary authenticated requests.
- `fintrak_refresh` is attached only to `/auth/refresh` and `/auth/logout`.
- Login and registration suppress the access token and do not attempt a
  credential-request refresh after a 401.
- Concurrent 401 responses share one refresh attempt and replay the failed
  request once. A second 401 is returned to the caller.
- Logout sends the refresh cookie so the backend can revoke the whole rotation
  family, then clears local state even if the network request fails.
- `AccessToken()` returns a live secret. Never log it or include it in an
  exported diagnostic.

The server stores refresh-token hashes and records rotation families; the
client never persists the refresh token to disk.

## Requests, downloads, and limits

The default JSON request timeout is 60 seconds. Statement parsing uses 90
seconds, backup restore uses 320 seconds, and streaming downloads use a
10-minute timeout. JSON responses are capped at 64 MiB. CSV, PDF, and backup
responses are streamed to the caller's writer instead of being buffered.

`SetTransport` replaces the HTTP transport and is the hook used by MCP to
install its read-only policy guard. Configure it before concurrent use. A nil
transport restores the default.

## Errors and filters

`APIError` carries the status, field errors, and a bounded raw response body.
Its `Error` and `Message` methods are safe for status lines. `StatusIs` matches
an `*APIError` directly; `Unauthorized` walks wrapped errors for callers that
add context.

List and CSV-export filters share one grammar, including sentinels, multi-value
filters, exact amounts, loan attachments, recurring state, and
`excludeAttached`. `excludeAttached` excludes loan attachments only; recurring
attachments are controlled separately by `recurringId` and `recurring`.

## Money and optional fields

`api.Amount` carries decimal major-unit text verbatim. The client does no
money arithmetic. `ParseAmount` mirrors the backend's strict user-input grammar;
`Float64` is display-only for graph or heatmap scaling. `OptionalUUID` and
`OptionalInt` distinguish absent, set, and explicit-null partial-update fields;
their request tags use `omitzero` so an untouched nullable field is not cleared.

## Development

Run from this directory:

```bash
go test ./...
go vet ./...
go build ./...
```

`spec_parity_test.go` compares the client operation table with
`../backend/openapi.yaml` in both directions and executes each route against a
stub asserting its method, path, and query arguments.
