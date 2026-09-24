#!/usr/bin/env bash
set -euo pipefail

# One-command release: verifies, tests, then tags and pushes to trigger the
# CI publish workflow (.github/workflows/docker-publish.yml).
#
# The gate below is deliberately not a restatement of CI's: it calls the same
# entry points the workflow's `validate` aggregate depends on (the Makefile
# coverage targets, `bun run test:coverage`, `uv lock --check`, the go.mod
# tidy checks and `make openapi-check`). Restating them is how the two drifted
# apart — `bun run test` passes where CI's `bun run test:coverage` fails on the
# v8 thresholds, and an untidy go.mod, a stale uv.lock or a coverage
# regression used to reach a tag that CI then rejected, leaving a public tag
# with no images behind it.
#
# Usage:
#   ./scripts/release.sh v1.2.3

VERSION="${1:-}"

if [[ -z "$VERSION" ]]; then
    echo "Usage: $0 vX.Y.Z" >&2
    exit 1
fi

if ! [[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "Error: version must match vX.Y.Z, got '$VERSION'" >&2
    exit 1
fi

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v make >/dev/null 2>&1; then
    echo "Error: GNU make is required - the gate reuses the Makefile targets CI runs" >&2
    exit 1
fi

# 1. Must be on master, with a clean tree, at the commit that is on origin/master
branch="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$branch" != "master" ]]; then
    echo "Error: releases must be cut from 'master' (currently on '$branch')" >&2
    exit 1
fi

# A clean tree means no unstaged AND no staged changes: `git diff` only
# compares the working tree to the index, so a `git add`ed change would
# otherwise be invisible here and silently omitted from the tag (which points
# at HEAD).
if ! git diff --quiet --exit-code; then
    echo "Error: working tree is dirty - commit or stash changes first" >&2
    exit 1
fi
if ! git diff --cached --quiet --exit-code; then
    echo "Error: staged changes exist - commit them before tagging a release" >&2
    exit 1
fi

# The tag is pushed on its own, and CI validates whatever commit it points at, so
# nothing else checks that the released commit is on the remote branch. Without
# this a stale or diverged local master publishes a tag whose code origin/master
# does not contain.
echo "==> Fetching origin/master"
if ! git fetch origin master; then
    echo "Error: could not fetch origin/master - is 'origin' configured?" >&2
    exit 1
fi

head_commit="$(git rev-parse HEAD)"
remote_commit="$(git rev-parse FETCH_HEAD)"
if [[ "$head_commit" != "$remote_commit" ]]; then
    echo "Error: HEAD ($head_commit) is not origin/master ($remote_commit)." >&2
    echo "       Push your commits, or pull origin master, before tagging a release." >&2
    exit 1
fi

# 2. Tag must not already exist
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null 2>&1; then
    echo "Error: tag $VERSION already exists" >&2
    exit 1
fi

cd "$ROOT"

# 3. Every Go module must be tidy (CI: "Verify go.mod is tidy" in all four jobs)
for module in backend client tui mcp; do
    echo "==> Verifying $module/go.mod is tidy"
    (
        cd "$module"
        go mod tidy
        if ! git diff --exit-code go.mod go.sum; then
            echo "Error: $module/go.mod or go.sum changed under 'go mod tidy' - commit the tidy result" >&2
            exit 1
        fi
    )
done

# 4. go vet, the parser lockfile, the coverage floors and the OpenAPI route
#    check - the Makefile targets and commands the CI jobs invoke, so the
#    numbers stay in one place.
echo "==> Running go vet (all four Go modules)"
make vet vet-client vet-tui vet-mcp

echo "==> Verifying uv.lock is up to date"
(cd "$ROOT/statement_parser" && uv lock --check)

echo "==> Enforcing coverage floors, OpenAPI parity, and documentation checks"
make test-cover-check test-client-cover-check test-tui-cover-check test-mcp-cover-check test-parser-cover-check openapi-check docs-check

# 5. Backend integration tests (Docker required; catches SQL pgxmock cannot)
echo "==> Running backend integration tests (Docker required)"
make test-integration

# 6. Frontend: the lockfile, typecheck, threshold-enforcing tests, production build
echo "==> Installing frontend dependencies from the lockfile"
(cd "$ROOT/frontend" && bun install --frozen-lockfile)

echo "==> Typechecking frontend"
(cd "$ROOT/frontend" && bun run typecheck)

echo "==> Running frontend tests with the coverage thresholds"
(cd "$ROOT/frontend" && bun run test:coverage)

echo "==> Building frontend"
(cd "$ROOT/frontend" && bun run build)

# 7. Tag and push (triggers the publish workflow)
echo "==> Tagging $VERSION"
git tag -a "$VERSION" -m "Release $VERSION"
git push origin "$VERSION"

echo "Released $VERSION - CI is building and publishing Docker images."
