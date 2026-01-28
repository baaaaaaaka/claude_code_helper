#!/usr/bin/env python3
import argparse
import json
import re
from pathlib import Path


def version_key(version: str) -> list[int]:
    return [int(part) for part in version.split(".")]


def load_records(table_path: Path) -> dict[str, str]:
    if not table_path.exists():
        return {}
    records: dict[str, str] = {}
    for line in table_path.read_text().splitlines():
        if not line.startswith("|"):
            continue
        parts = [part.strip() for part in line.strip().strip("|").split("|")]
        if len(parts) < 2:
            continue
        version = parts[0].lstrip("v")
        if re.match(r"^[0-9]+(\.[0-9]+)+$", version):
            records[version] = parts[1]
    return records


def render_table(records: dict[str, str]) -> str:
    header = [
        "# Claude Code compatibility",
        "",
        "Rows are added automatically after tests pass for a Claude Code release.",
        "",
        "| Claude Code version | claude-proxy tag |",
        "| --- | --- |",
    ]
    rows = [
        f"| {version} | {records[version]} |"
        for version in sorted(records, key=version_key)
    ]
    return "\n".join(header + rows) + "\n"


def update_file(path: Path, content: str) -> bool:
    existing = path.read_text() if path.exists() else ""
    if existing == content:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    return True


def update_ci_version(path: Path, latest_version: str) -> bool:
    text = path.read_text()
    updated, count = re.subn(
        r'(CLAUDE_PATCH_VERSION:\s*")([^"]+)(")',
        rf'\1{latest_version}\3',
        text,
    )
    if count == 0:
        raise RuntimeError(f"CLAUDE_PATCH_VERSION not found in {path}")
    if updated == text:
        return False
    path.write_text(updated)
    return True


def update_test_version(path: Path, latest_version: str) -> bool:
    text = path.read_text()
    updated, count = re.subn(
        r'(const\s+defaultClaudePatchVersion\s*=\s*")([^"]+)(")',
        rf'\1{latest_version}\3',
        text,
    )
    if count == 0:
        raise RuntimeError(f"defaultClaudePatchVersion not found in {path}")
    if updated == text:
        return False
    path.write_text(updated)
    return True


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Update Claude Code compatibility table and test versions."
    )
    parser.add_argument("--missing-json", default="[]", help="JSON array of versions")
    parser.add_argument("--latest-version", default="", help="Latest Claude Code version")
    parser.add_argument("--proxy-tag", default="", help="claude-proxy tag")
    parser.add_argument(
        "--table-path",
        default="docs/claude_code_compatibility.md",
        help="Compatibility table path",
    )
    parser.add_argument(
        "--ci-path", default=".github/workflows/ci.yml", help="CI workflow path"
    )
    parser.add_argument(
        "--test-file",
        default="internal/cli/claude_patch_integration_test.go",
        help="Integration test file path",
    )
    args = parser.parse_args()

    try:
        missing = json.loads(args.missing_json) if args.missing_json else []
    except json.JSONDecodeError as exc:
        raise SystemExit(f"Invalid JSON for --missing-json: {exc}") from exc

    if not args.proxy_tag:
        raise SystemExit("--proxy-tag is required")

    table_path = Path(args.table_path)
    records = load_records(table_path)
    for version in missing:
        version = str(version).lstrip("v")
        if re.match(r"^[0-9]+(\.[0-9]+)+$", version):
            records[version] = args.proxy_tag

    table_updated = update_file(table_path, render_table(records))

    ci_updated = False
    test_updated = False
    if args.latest_version:
        ci_updated = update_ci_version(Path(args.ci_path), args.latest_version)
        test_updated = update_test_version(Path(args.test_file), args.latest_version)

    if table_updated or ci_updated or test_updated:
        print("updated")
    else:
        print("no changes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
