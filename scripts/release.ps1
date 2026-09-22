# One-command release: verifies, tests, then tags and pushes to trigger the
# CI publish workflow (.github/workflows/docker-publish.yml).
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

# 1. Must be on master with a clean working tree
$branch = git rev-parse --abbrev-ref HEAD
if ($LASTEXITCODE -ne 0) { throw "Not a git repository" }
if ($branch -ne 'master') { throw "Releases must be cut from 'master' (currently on '$branch')" }

git diff --quiet --exit-code
if ($LASTEXITCODE -ne 0) { throw "Working tree is dirty - commit or stash changes first" }

# 2. Tag must not already exist
git rev-parse -q --verify "refs/tags/$Version" 2>$null
if ($LASTEXITCODE -eq 0) { throw "Tag $Version already exists" }

# 3. Backend: vet, unit tests, integration tests (mirrors the CI gate)
Push-Location "$repoRoot\backend"
try {
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "go vet failed" }

    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "Backend tests failed" }

    go test -tags=integration -count=1 ./...
    if ($LASTEXITCODE -ne 0) { throw "Backend integration tests failed (Docker required)" }
} finally {
    Pop-Location
}

# 4. Frontend: typecheck, tests, production build
Push-Location "$repoRoot\frontend"
try {
    bun run typecheck
    if ($LASTEXITCODE -ne 0) { throw "Frontend typecheck failed" }

    bun run test
    if ($LASTEXITCODE -ne 0) { throw "Frontend tests failed" }

    bun run build
    if ($LASTEXITCODE -ne 0) { throw "Frontend build failed" }
} finally {
    Pop-Location
}

# 5. Statement parser tests
Push-Location "$repoRoot\statement_parser"
try {
    uv run --frozen python -m unittest discover -s tests -v
    if ($LASTEXITCODE -ne 0) { throw "Statement parser tests failed" }
} finally {
    Pop-Location
}

# 6. Go clients: shared client, TUI, MCP server (mirrors the CI gate)
Push-Location "$repoRoot\client"
try {
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "go vet (client) failed" }

    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "Shared client tests failed" }
} finally {
    Pop-Location
}

Push-Location "$repoRoot\mcp"
try {
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "go vet (mcp) failed" }

    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "MCP server tests failed" }
} finally {
    Pop-Location
}

Push-Location "$repoRoot\tui"
try {
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "go vet (tui) failed" }

    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "TUI tests failed" }
} finally {
    Pop-Location
}

# 7. Tag and push (triggers the publish workflow)
git tag -a $Version -m "Release $Version"
if ($LASTEXITCODE -ne 0) { throw "Failed to create tag $Version" }

git push origin $Version
if ($LASTEXITCODE -ne 0) { throw "Failed to push tag $Version" }

Write-Host "Released $Version - CI is building and publishing Docker images."
