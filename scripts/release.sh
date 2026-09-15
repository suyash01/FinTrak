#!/usr/bin/env bash
set -euo pipefail

# One-command release: verifies, tests, then tags and pushes to trigger the
# CI publish workflow (.github/workflows/docker-publish.yml).
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

# 1. Must be on master with a clean working tree
branch="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$branch" != "master" ]]; then
    echo "Error: releases must be cut from 'master' (currently on '$branch')" >&2
    exit 1
fi

if ! git diff --quiet --exit-code; then
    echo "Error: working tree is dirty - commit or stash changes first" >&2
    exit 1
fi

# 2. Tag must not already exist
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null 2>&1; then
    echo "Error: tag $VERSION already exists" >&2
    exit 1
fi

# 3. Backend: vet, unit tests, integration tests (mirrors the CI gate)
echo "==> Running go vet"
(cd "$ROOT/backend" && go vet ./...)

echo "==> Running backend tests"
(cd "$ROOT/backend" && go test ./...)

echo "==> Running backend integration tests (Docker required)"
(cd "$ROOT/backend" && go test -tags=integration -count=1 ./...)

# 4. Frontend: typecheck, tests, production build
echo "==> Typechecking frontend"
(cd "$ROOT/frontend" && bun run typecheck)

echo "==> Running frontend tests"
(cd "$ROOT/frontend" && bun run test)

echo "==> Building frontend"
(cd "$ROOT/frontend" && bun run build)

# 5. Statement parser tests
echo "==> Running statement parser tests"
(cd "$ROOT/statement_parser" && uv run --frozen python -m unittest discover -s tests -v)

# 6. Tag and push (triggers the publish workflow)
echo "==> Tagging $VERSION"
git tag -a "$VERSION" -m "Release $VERSION"
git push origin "$VERSION"

echo "Released $VERSION - CI is building and publishing Docker images."
