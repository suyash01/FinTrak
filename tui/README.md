# FinTrak Terminal Client

The TUI is a Go + Bubble Tea client for the same REST API as the web SPA. It
can run in a local terminal or serve the same UI through an SSH door.

## Local mode

```bash
go run .
go run . -api https://fintrak.example.com/api/v1
```

`-api` defaults to `FINTRAK_API_URL` and then the client default. The local
process prompts for FinTrak credentials. `go test ./...`, `go vet ./...`, and
`go build ./...` verify the module.

## SSH mode

```bash
go run . -ssh -ssh-addr :2222 -ssh-auth password
ssh -p 2222 localhost
```

The SSH door uses these options (each has the corresponding `FINTRAK_SSH_*`
environment fallback where applicable):

- `-ssh-addr` / `FINTRAK_SSH_ADDR` — listen address, default `:2222`.
- `-ssh-host-key` / `FINTRAK_SSH_HOST_KEY` — persistent host-key path; it is
  created on first use and must remain stable.
- `-ssh-auth` / `FINTRAK_SSH_AUTH` — `password`, `key`, or `any`.
- `-ssh-authorized-keys` / `FINTRAK_SSH_AUTHORIZED_KEYS` — required for `key`
  and `any`.
- `-ssh-idle-timeout` / `FINTRAK_SSH_IDLE_TIMEOUT` — idle limit, default 15m.
- `-ssh-max-timeout` / `FINTRAK_SSH_MAX_TIMEOUT` — absolute limit, default 12h;
  zero disables it.
- `-ssh-max-sessions` / `FINTRAK_SSH_MAX_SESSIONS` — session cap, default 32.
- `-ssh-auth-per-minute` / `FINTRAK_SSH_AUTH_PER_MINUTE` — per-remote-address
  authentication throttle, default 10.
- `-log-file` / `FINTRAK_LOG_FILE` and `-log-level` / `FINTRAK_LOG_LEVEL` —
  logging controls.

Password SSH authentication exchanges the SSH email/password for an API session
and opens the TUI signed in. Public-key authentication only opens the door; the
TUI then shows its normal sign-in screen. `any` accepts either method.

The door deliberately refuses TCP forwarding, agent forwarding, SFTP and other
subsystems, and unknown channel requests. Session and authentication limits
protect the API because every SSH user otherwise arrives from the door's IP.

## UI architecture

`internal/ui.App` owns the session, reference-data cache, sidebar, panes, and
overlays. Screens register themselves from their own files and are created once
per session, so cursors and filters survive navigation. Key presses are sent
only to the visible screen; tagged data messages are namespaced per screen.
`tab` switches between sidebar and content focus, while deliberate navigation
commands enter a screen. Form, confirmation, picker, and information overlays
are owned by the app rather than by individual screens.

`internal/sshd/zz_real_test.go` is the only opt-in real-API test and is skipped
unless `FINTRAK_REAL_API_URL` is set. All other tests use local HTTP stubs.
