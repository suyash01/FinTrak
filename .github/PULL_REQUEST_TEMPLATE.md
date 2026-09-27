<!--
  Title: one line, Conventional Commits style, e.g.
  "feat(import): read ISO 20022 and OFX bank files in the browser"
  Commit subjects inside the PR use the same style.
-->

Closes #

## What and why

<!-- What does this change, and what problem does it solve? -->

## Checklist

- [ ] I opened an issue for this, or it is a small fix that needs none
- [ ] `make test` + the `make test-*-cover-check` targets for every module I touched
- [ ] `make test-integration` (if I touched SQL, migrations, or anything Docker-backed)
- [ ] `make openapi-check` (if I added, changed, or removed a route)
- [ ] `make docs-check` (if I edited any Markdown)
- [ ] `bun run typecheck` + `bun run test:coverage` + `bun run build` (if I touched `frontend/`)
- [ ] `go mod tidy` leaves `go.mod` / `go.sum` unchanged, or the tidy result is committed
- [ ] New behaviour has tests; no coverage floor was lowered to make this pass
- [ ] Schema changes are **new** `NNNNNN_*.up.sql` / `.down.sql` migrations, not edits to existing ones
- [ ] No secrets, no `.env`, no real transaction data in the diff

## What I deliberately left out

<!--
Anything scoped out, deferred, or done differently than proposed - and why.
Reviewers should not have to guess what you considered and rejected.
-->

## Notes for the reviewer

<!--
Anything non-obvious: a behaviour change on a shared path, a trade-off you were unsure
about, a place you would especially like a second opinion. Optional.
-->
