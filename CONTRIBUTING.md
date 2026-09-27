# Contributing to FinTrak

Thanks for your interest in improving FinTrak. This document covers how to set the
project up, what CI will check, and what a good pull request looks like.

**The codebase's own rules live elsewhere on purpose.** This file is the human-facing
entry point — setup, the verification gate, and PR expectations. It deliberately does
*not* restate the code-level conventions, so the two can never drift apart:

| For | See |
| --- | --- |
| Code conventions, architecture rules, non-negotiables | [`AGENTS.md`](AGENTS.md) |
| Runtime components, trust boundaries, domain model | [`docs/architecture.md`](docs/architecture.md) |
| Transaction lifecycle, ER diagram, flowcharts | [`FLOWCHART.md`](FLOWCHART.md) |
| Deploying and operating an instance | [`docs/operations.md`](docs/operations.md) |
| Features, setup, API overview, project layout | [`README.md`](README.md) |
| The API contract | [`backend/openapi.yaml`](backend/openapi.yaml) |

If a change touches any of those areas, read the relevant file first. The
non-negotiables in `AGENTS.md` (integer minor units for money, migrations only, never
NULL tags, the date window) are the ones most likely to be violated by accident.

---

## Before you start

- **Open an issue first for anything non-trivial** — a new feature, a schema change, a
  dependency bump, or a behavioural change to an existing endpoint. A short discussion
  first is cheaper than a rejected 2,000-line PR.
- **Small fixes can go straight to a PR.** Typo fixes, a missing test, a one-line
  correction to a doc, and unambiguous bug fixes need no prior issue.
- **Please don't open a pull request you have not run the gate on** (below). CI runs the
  same commands, so a red PR is just a slower local run.

FinTrak is licensed under the [GNU AGPL-3.0](LICENSE). There is no CLA to sign: by
opening a pull request you agree that your contribution is distributed under that same
license.

---

## Getting set up

### Prerequisites

| Tool | Version | Needed for |
| --- | --- | --- |
| [Docker](https://www.docker.com/) & Compose | current | the dev stack |
| [Go](https://go.dev/dl/) | 1.27 | `backend/`, `client/`, `tui/`, `mcp/` |
| [bun](https://bun.sh/) | 1.4.2 | `frontend/` (pinned in `frontend/Dockerfile`) |
| [uv](https://docs.astral.sh/uv/) | current | `statement_parser/` |
| Python | 3.12+ | runs `scripts/check-docs.py` |

### Start the dev stack

```bash
git clone https://github.com/suyash01/FinTrak.git
cd FinTrak
make dev
```

| Service | URL |
| --- | --- |
| Frontend (SPA) | <http://localhost:3000> |
| API | <http://localhost:8080/api/v1> |
| Statement parser | <http://localhost:5000> |
| Adminer / pgAdmin | <http://localhost:8081> / <http://localhost:8082> |
| Log viewer (`--profile debug`) | <http://localhost:8083> |
| TUI over SSH (`--profile tui`) | `ssh -p 2222 localhost` |

Every published dev port is bound to `127.0.0.1`, so the dev stack is not reachable
from another machine. Stop it with `make dev-down`.

You do not need a running stack to work on most code: the backend's unit tests, the
client's, the TUI's, the MCP server's and the frontend's all run without one, and the
backend's tests need no database at all.

### The four Go modules

`backend/`, `client/`, `tui/` and `mcp/` are **separate Go modules** (`client/` and
`mcp/` are wired to the others with a local `replace`). A `go test ./...` in one does
not cover the rest, and CI runs each independently:

```bash
cd backend && go test ./...
cd client  && go test ./...
cd tui     && go test ./...
cd mcp     && go test ./...
```

---

## The verification gate

Run this before you open a pull request. It is the same gate `make release` runs, and
CI runs these commands inline — so a green local run means a green PR:

```bash
# Tidy: CI fails if `go mod tidy` changes go.mod / go.sum in any module
(cd backend && go mod tidy) && (cd client && go mod tidy) \
  && (cd tui && go mod tidy) && (cd mcp && go mod tidy)
git diff --exit-code '*/go.mod' '*/go.sum'

make vet vet-client vet-tui vet-mcp
(cd statement_parser && uv lock --check)

make test-cover-check test-client-cover-check test-tui-cover-check \
     test-mcp-cover-check test-parser-cover-check openapi-check docs-check
make test-integration          # Docker required

(cd frontend && bun install --frozen-lockfile)
(cd frontend && bun run typecheck)
(cd frontend && bun run test:coverage)
(cd frontend && bun run build)
```

`make help` lists every target.

### Coverage floors are enforced, not aspirational

Each part of the monorepo has its own floor, and a regression fails the build. The
numbers live in exactly one place each — the `min`/`--fail-under` argument in the
`Makefile` and the matching target in `codecov.yml`, which must stay equal.

| Part | Floor | Enforced by |
| --- | --- | --- |
| `backend/` | 85% | `make test-cover-check` |
| `client/` | 85% | `make test-client-cover-check` |
| `statement_parser/` | 90% | `make test-parser-cover-check` |
| `mcp/` | 80% | `make test-mcp-cover-check` |
| `frontend/` | lines 78, statements 76, branches 67, functions 71 | `bun run test:coverage` |
| `tui/` | 18% | `make test-tui-cover-check` |

The TUI floor is low on purpose: its render-heavy screens in `internal/ui` are what the
number measures, and it is meant to be ratcheted up as screen tests land. Do not lower
a floor to get a pull request merged.

### The API contract and the docs are checked too

- **Route parity.** `make openapi-check` fails if a route is registered in
  `backend/main.go` but missing from `backend/openapi.yaml` — *or* if the spec
  advertises a route that no longer exists. Adding an endpoint therefore means editing
  both, and a test will hold you to it. The same applies to the two Go clients, whose
  own parity suite fails until they claim the new operation.
- **Documentation.** `make docs-check` validates every relative Markdown link, Mermaid
  blocks, that every `make` target mentioned in the docs is a real `.PHONY` target, and
  that every OpenAPI operation has a summary. Run it after editing any Markdown.

---

## Branches, commits, and PR titles

- **Branch from `master`** and target your pull request at `master`. Release tags are
  cut only from `master` at `origin/master` with a clean tree, so nothing targeting
  another branch can ship.
- **Name branches after the ticket** — `feature/FIN-<n>`, the pattern already in use
  here. A branch for work with no ticket yet uses a descriptive slug instead
  (`feature/query-language`). Dependabot's `dependabot/…` branches are the third form.
- **Commit subjects follow Conventional Commits**, as the history already does —
  `feat(import): …`, `fix(tui): …`, `docs: …`, `test: …`, with an optional scope. The
  imperative mood reads best: `fix(links): reject a transfer to a closed account`.
- **Pull request titles are `<ticket-id>: <short description>`** — for example
  `FIN-7: read ISO 20022 and OFX bank files in the browser`. The description stays on
  one line, in plain prose, with no scope parentheses. This is **enforced in CI** by
  [`.github/workflows/pull-request-title.yml`](.github/workflows/pull-request-title.yml),
  which checks the shape `ABC-123: …` and a 100-character limit. Dependabot's
  auto-generated pull requests are exempt.

The commit subject and the PR title are two different things and follow two different
rules — a conventional-commit *commit* inside a ticket-prefixed *pull request* is
correct and expected.

---

## What a good pull request looks like

- **One concern per pull request.** A refactor bundled with a feature doubles the review
  cost and the risk. Land the refactor first.
- **Say what and why in the description**, and what you deliberately left out. The
  template asks for this.
- **Add the tests with the change.** New behaviour without a test cannot hold a coverage
  floor, and the handler tests in this repo are database-free by design — follow the
  existing `newTestServer` / `pgxmock` pattern rather than inventing a new one.
- **Schema changes are new migrations.** Add `NNNNNN_<name>.up.sql` and
  `.down.sql` in `backend/db/migrations`; never edit an existing migration, because
  deployed databases have already run it.
- **Never commit secrets.** `.env` and `.env.*` are ignored and must stay that way, and
  `.dockerignore` keeps them out of the build context a remote builder receives. Use
  `.env.example` to document a new variable.
- **Do not weaken a check to get your change in.** No deleted tests, no lowered
  coverage floor, no `skip` added to a suite that is red for a reason you have not
  explained. If a floor or a test genuinely needs to change, say why in the description
  — that is a reviewable decision, a silently weakened gate is not.
- **CI must be green** before it is merged.

---

## Reporting a vulnerability

**Do not open a public issue for a security problem.** FinTrak holds a real financial
ledger, so a disclosed vulnerability is a disclosure of someone's data.

This repository does not publish a security contact address, so use GitHub's private
channel: open the repository's **Security** tab and choose **Report a vulnerability**.
That opens a private advisory that only you and the maintainers can read. If that
option is unavailable to you, open an issue describing the *impact* without a
proof-of-concept and ask for a private channel in it.

Please include: the affected version or commit, what an attacker can reach, and
reproduction steps. Give the maintainers a reasonable window to ship a fix before
disclosing publicly.

---

## Reporting a bug

Use the **bug report** issue template. The fields that help most:

- Which part of the stack it is in (backend, frontend, TUI, MCP, parser).
- The **exact version** — the release tag, or `git rev-parse --short HEAD` on `master`.
- The steps to reproduce, and what you expected instead.
- The relevant log output. `LOG_LEVEL=debug` and a positive `LOG_BODY_LIMIT` turn on
  request/response body capture, which helps a lot with an API bug.

**Never paste a `.env`, a `JWT_SECRET`, a `TOKEN_ENCRYPTION_KEY`, a Paperless API
token, or your real transaction data into an issue.** A minimal reproduction with
invented accounts is faster to help with anyway.

---

## Suggesting a feature

Use the **feature request** template. Lead with the problem, not the solution — the
maintainer may know that a different approach already exists or that the data model
makes the proposed one awkward. There is no Discussions board on this repository yet, so
the "alternatives you considered" field is where that conversation happens.
