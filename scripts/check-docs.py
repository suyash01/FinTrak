#!/usr/bin/env python3
"""Small, dependency-free documentation and contract consistency checks."""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MARKDOWN_LINK = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
PATH_TARGET = re.compile(r"^[^:#?]+(?:#[^?]*)?(?:\?.*)?$")
OPENAPI_PATH = re.compile(r"^  (/[^:]*):\s*$", re.MULTILINE)
OPENAPI_METHOD = re.compile(r"^    (get|post|put|patch|delete):\s*$", re.MULTILINE)
MAKE_TARGET = re.compile(r"\bmake\s+([A-Za-z0-9_-]+)")
MERMAID_TYPES = {
    "classDiagram",
    "erDiagram",
    "flowchart",
    "gantt",
    "graph",
    "journey",
    "mindmap",
    "quadrantChart",
    "sequenceDiagram",
    "stateDiagram",
    "stateDiagram-v2",
    "timeline",
}


def markdown_files() -> list[Path]:
    files: list[Path] = []
    for name in ("README.md", "AGENTS.md", "FLOWCHART.md"):
        path = ROOT / name
        if path.exists():
            files.append(path)
    for base in (ROOT / "docs", ROOT / "client", ROOT / "tui", ROOT / "mcp", ROOT / "frontend", ROOT / "backend/db/migrations"):
        if base.exists():
            files.extend(
                path
                for path in base.rglob("*.md")
                if not any(part in {".git", "node_modules", ".venv", "venv"} for part in path.parts)
            )
    return sorted(set(files))


def check_links(errors: list[str]) -> None:
    for path in markdown_files():
        text = path.read_text(encoding="utf-8")
        for raw in MARKDOWN_LINK.findall(text):
            target = raw.strip().split(maxsplit=1)[0].strip("<>")
            if not target or target.startswith(("#", "http://", "https://", "mailto:", "tel:")):
                continue
            target = target.split("#", 1)[0].split("?", 1)[0]
            if not target or not PATH_TARGET.match(target):
                continue
            candidate = (ROOT / target[1:]) if target.startswith("/") else (path.parent / target)
            if not candidate.exists():
                errors.append(f"{path.relative_to(ROOT)}: missing local link target {raw!r}")


def check_mermaid(errors: list[str]) -> None:
    for path in markdown_files():
        lines = path.read_text(encoding="utf-8").splitlines()
        in_block = False
        block: list[str] = []
        start = 0
        for number, line in enumerate(lines, 1):
            if not in_block and line.strip() == "```mermaid":
                in_block = True
                start = number
                block = []
            elif in_block and line.strip() == "```":
                first = next((item.strip() for item in block if item.strip()), "")
                if not first or first.split(maxsplit=1)[0] not in MERMAID_TYPES:
                    errors.append(f"{path.relative_to(ROOT)}:{start}: Mermaid block has no recognized diagram declaration")
                if sum(item.strip().startswith("subgraph") for item in block) != sum(item.strip() == "end" for item in block):
                    errors.append(f"{path.relative_to(ROOT)}:{start}: Mermaid subgraph/end blocks are unbalanced")
                in_block = False
            elif in_block:
                block.append(line)
        if in_block:
            errors.append(f"{path.relative_to(ROOT)}:{start}: unterminated Mermaid block")


def check_makefile_and_readme(errors: list[str]) -> None:
    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    phony_match = re.search(r"^\.PHONY:\s*(.+)$", makefile, re.MULTILINE)
    if not phony_match:
        errors.append("Makefile has no .PHONY declaration")
        return
    phony = set(phony_match.group(1).split())
    help_text = makefile[makefile.find("help:"):makefile.find("dev:", makefile.find("help:"))]
    for target in sorted(phony):
        if target not in help_text:
            errors.append(f"Makefile target {target!r} is missing from help")
    readme = (ROOT / "README.md").read_text(encoding="utf-8")
    for target in set(MAKE_TARGET.findall(readme)):
        if target not in phony:
            errors.append(f"README mentions make {target}, but it is not a .PHONY target")


def check_openapi_descriptions(errors: list[str]) -> None:
    for relative in ("backend/openapi.yaml", "statement_parser/openapi.yaml"):
        path = ROOT / relative
        text = path.read_text(encoding="utf-8")
        matches = list(OPENAPI_PATH.finditer(text))
        for index, match in enumerate(matches):
            end = matches[index + 1].start() if index + 1 < len(matches) else len(text)
            block = text[match.end():end]
            methods = list(OPENAPI_METHOD.finditer(block))
            if not methods:
                errors.append(f"{relative}: path {match.group(1)} has no HTTP operation")
            for method in methods:
                operation_end = methods[methods.index(method) + 1].start() if methods.index(method) + 1 < len(methods) else len(block)
                operation = block[method.end():operation_end]
                if not re.search(r"^\s{6}(summary|description):", operation, re.MULTILINE):
                    errors.append(f"{relative}: {match.group(1)} {method.group(1).upper()} has no summary or description")


def main() -> int:
    errors: list[str] = []
    check_links(errors)
    check_mermaid(errors)
    check_makefile_and_readme(errors)
    check_openapi_descriptions(errors)
    if errors:
        print("Documentation checks failed:")
        for error in errors:
            print(f"- {error}")
        return 1
    print("Documentation checks passed.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
