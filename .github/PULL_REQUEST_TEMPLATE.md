<!--
  Title must be: <ticket-id>: <short description>   e.g. "FIN-7: read ISO 20022 bank files"
  Checked by .github/workflows/pull-request-title.yml.
  Commit subjects inside the PR follow Conventional Commits (feat(x):, fix(x):, docs:, test:).
-->

## What and why

<!-- What does this change, and what problem does it solve? Link the issue if there is one. -->

Closes #

## Checklist

- [ ] I opened or referenced an issue for this (or it is a small fix that does not need one)
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
