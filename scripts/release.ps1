# One-command release: verifies, tests, then tags and pushes to trigger the
# CI publish workflow (.github/workflows/docker-publish.yml).
#
# The gate below is deliberately not a restatement of CI's: it calls the same
# entry points the workflow's `validate` aggregate depends on (the Makefile
# coverage targets, `bun run test:coverage`, `uv lock --check`, the go.mod
# tidy checks and `make openapi-check`). Restating them is how the two drifted
# apart - `bun run test` passes where CI's `bun run test:coverage` fails on the
# v8 thresholds, and an untidy go.mod, a stale uv.lock or a coverage
# regression used to reach a tag that CI then rejected, leaving a public tag
# with no images behind it. scripts/release.sh runs the same sequence.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts/release.ps1 v1.2.3

param(
    [Parameter(Mandatory = $true)]
    [ValidatePattern('^v[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$Version
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command make -ErrorAction SilentlyContinue)) {
    throw "GNU make is required - the gate reuses the Makefile targets CI runs"
}

# 1. Must be on master, with a clean tree, at the commit that is on origin/master
$branch = git rev-parse --abbrev-ref HEAD
if ($LASTEXITCODE -ne 0) { throw "Not a git repository" }
if ($branch -ne 'master') { throw "Releases must be cut from 'master' (currently on '$branch')" }

git diff --quiet --exit-code
if ($LASTEXITCODE -ne 0) { throw "Working tree is dirty - commit or stash changes first" }

# `git diff` only compares the working tree to the index: a `git add`ed change
# would be invisible here and silently omitted from the tag (which points at HEAD).
git diff --cached --quiet --exit-code
if ($LASTEXITCODE -ne 0) { throw "Staged changes exist - commit them before tagging a release" }

# The tag is pushed on its own, and CI validates whatever commit it points at, so
# nothing else checks that the released commit is on the remote branch. Without
# this a stale or diverged local master publishes a tag whose code origin/master
# does not contain.
Write-Host "==> Fetching origin/master"
git fetch origin master
if ($LASTEXITCODE -ne 0) { throw "Could not fetch origin/master - is 'origin' configured?" }

$headCommit = (git rev-parse HEAD).Trim()
$remoteCommit = (git rev-parse FETCH_HEAD).Trim()
if ($headCommit -ne $remoteCommit) {
    throw "HEAD ($headCommit) is not origin/master ($remoteCommit). Push your commits, or pull origin master, before tagging a release."
}

# 2. Tag must not already exist
git rev-parse -q --verify "refs/tags/$Version" 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) { throw "Tag $Version already exists" }

Push-Location $repoRoot
try {
    # 3. Every Go module must be tidy (CI: "Verify go.mod is tidy" in all four jobs)
    foreach ($module in @('backend', 'client', 'tui', 'mcp')) {
        Write-Host "==> Verifying $module/go.mod is tidy"
        Push-Location "$repoRoot\$module"
        try {
            go mod tidy
            if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed in $module" }

            git diff --exit-code go.mod go.sum
            if ($LASTEXITCODE -ne 0) { throw "$module/go.mod or go.sum changed under 'go mod tidy' - commit the tidy result" }
        } finally {
            Pop-Location
        }
    }

    # 4. go vet, the parser lockfile, the coverage floors and the OpenAPI route
    #    check - the Makefile targets and commands the CI jobs invoke, so the
    #    numbers stay in one place.
    Write-Host "==> Running go vet (all four Go modules)"
    make vet vet-client vet-tui vet-mcp
    if ($LASTEXITCODE -ne 0) { throw "go vet failed" }

    Write-Host "==> Verifying uv.lock is up to date"
    Push-Location "$repoRoot\statement_parser"
    try {
        uv lock --check
        if ($LASTEXITCODE -ne 0) { throw "uv.lock is out of date - run 'uv lock' and commit the result" }
    } finally {
        Pop-Location
    }

    Write-Host "==> Enforcing coverage floors, OpenAPI parity, and documentation checks"
    make test-cover-check test-client-cover-check test-tui-cover-check test-mcp-cover-check test-parser-cover-check openapi-check docs-check
    if ($LASTEXITCODE -ne 0) { throw "A coverage floor, OpenAPI route check, or documentation check failed" }

    # 5. Backend integration tests (Docker required; catches SQL pgxmock cannot)
    Write-Host "==> Running backend integration tests (Docker required)"
    make test-integration
    if ($LASTEXITCODE -ne 0) { throw "Backend integration tests failed (Docker required)" }

    # 6. Frontend: the lockfile, typecheck, threshold-enforcing tests, build
    Push-Location "$repoRoot\frontend"
    try {
        Write-Host "==> Installing frontend dependencies from the lockfile"
        bun install --frozen-lockfile
        if ($LASTEXITCODE -ne 0) { throw "bun install --frozen-lockfile failed - the lockfile is out of sync" }

        Write-Host "==> Typechecking frontend"
        bun run typecheck
        if ($LASTEXITCODE -ne 0) { throw "Frontend typecheck failed" }

        Write-Host "==> Running frontend tests with the coverage thresholds"
        bun run test:coverage
        if ($LASTEXITCODE -ne 0) { throw "Frontend tests failed (or a coverage threshold regressed)" }

        Write-Host "==> Building frontend"
        bun run build
        if ($LASTEXITCODE -ne 0) { throw "Frontend build failed" }
    } finally {
        Pop-Location
    }

    # 7. Tag and push (triggers the publish workflow)
    Write-Host "==> Tagging $Version"
    git tag -a $Version -m "Release $Version"
    if ($LASTEXITCODE -ne 0) { throw "Failed to create tag $Version" }

    git push origin $Version
    if ($LASTEXITCODE -ne 0) { throw "Failed to push tag $Version" }
} finally {
    Pop-Location
}

Write-Host "Released $Version - CI is building and publishing Docker images."
