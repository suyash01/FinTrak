# task-brief.ps1 — extract a plan task to a UTF-8 brief file, byte-safe.
#
# Set-Content uses the console's default encoding, which silently replaces the
# UTF-8 em dashes in the plan's comments with hyphens. [System.IO.File] writes
# are encoding-explicit, so the brief a subagent reads is byte-identical to the
# plan it came from.
#
# Usage: ./scripts/task-brief.ps1 PLAN_FILE TASK_NUMBER OUT_DIR
param(
  [Parameter(Mandatory = $true)][string]$PlanFile,
  [Parameter(Mandatory = $true)][int]$TaskNumber,
  [Parameter(Mandatory = $true)][string]$OutDir
)

$lines = [System.IO.File]::ReadAllLines($PlanFile)
$startPat = "^### Task ${TaskNumber}: "
$start = ($lines | Select-String -Pattern $startPat | Select-Object -First 1).LineNumber
if (-not $start) { throw "no heading for Task ${TaskNumber} in ${PlanFile}" }

# End at the next task heading, or at the Execution Order section.
$next = ($lines | Select-String -Pattern '^### Task ' | Where-Object { $_.LineNumber -gt $start } | Select-Object -First 1).LineNumber
if ($next) { $end = $next } else {
  $eo = ($lines | Select-String -Pattern '^## Execution Order' | Select-Object -First 1).LineNumber
  if (-not $eo) { throw "Task ${TaskNumber} has no end: no next task and no Execution Order heading" }
  $end = $eo
}

$title = $lines[$start - 1] -replace '^### Task \d+: ', ''
$header = @(
  "# Task $TaskNumber brief - $title",
  "",
  "Extracted verbatim from the plan. Read this first: it is your requirements,",
  "with the exact values to use verbatim.",
  ""
)
$body = $header + $lines[($start - 2)..($end - 3)]

$out = Join-Path $OutDir "task-$TaskNumber-brief.md"
[System.IO.File]::WriteAllLines($out, $body, (New-Object System.Text.UTF8Encoding $false))

# Prove the encoding survived rather than trusting it.
$text = [System.Text.Encoding]::UTF8.GetString([System.IO.File]::ReadAllBytes($out))
$fffd = ([regex]::Matches($text, [char]0xFFFD)).Count
$emdash = ([regex]::Matches($text, [char]0x2014)).Count
Write-Output "wrote $out ($($body.Length) lines, $fffd replacement chars, $emdash em dashes)"
if ($fffd -gt 0) { throw "encoding was corrupted: $fffd U+FFFD in the brief" }
