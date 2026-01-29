#!/usr/bin/env python3
import argparse
import json
import re
from pathlib import Path

PLATFORM_COLUMNS = [
    "linux",
    "mac",
    "windows",
    "rockylinux8",
    "ubuntu20.04",
]
TABLE_HEADER = ["Claude Code version", "claude-proxy tag", *PLATFORM_COLUMNS]


def version_key(version: str) -> list[int]:
    return [int(part) for part in version.split(".")]


def is_version(value: str) -> bool:
    return re.match(r"^[0-9]+(\.[0-9]+)+$", value) is not None


def normalize_status(value: str) -> str:
    if str(value).strip().lower() == "pass":
        return "pass"
    return "fail"


def load_records(table_path: Path) -> dict[str, dict[str, str]]:
    if not table_path.exists():
        return {}
    records: dict[str, dict[str, str]] = {}
    columns: list[str] = []
    for line in table_path.read_text().splitlines():
        if not line.startswith("|"):
            continue
        parts = [part.strip() for part in line.strip().strip("|").split("|")]
        if len(parts) < 2:
            continue
        if not columns:
            columns = parts
            continue
        if all(re.match(r"^-+$", part) for part in parts):
            continue
        row = {columns[i]: parts[i] for i in range(min(len(columns), len(parts)))}
        version = row.get("Claude Code version", "").lstrip("v")
        if not is_version(version):
            continue
        record: dict[str, str] = {}
        tag = row.get("claude-proxy tag", "").strip()
        if tag:
            record["tag"] = tag
        for platform in PLATFORM_COLUMNS:
            if platform in row and row[platform].strip():
                record[platform] = normalize_status(row[platform])
        records[version] = record
    return records


def render_table(records: dict[str, dict[str, str]]) -> str:
    header = [
        "# Claude Code compatibility",
        "",
        "Rows are added automatically after tests pass for a Claude Code release.",
        "",
        "| " + " | ".join(TABLE_HEADER) + " |",
        "| " + " | ".join("---" for _ in TABLE_HEADER) + " |",
    ]
    rows = [
        "| "
        + " | ".join(
            [
                version,
                records[version].get("tag", ""),
                *(records[version].get(platform, "") for platform in PLATFORM_COLUMNS),
            ]
        )
        + " |"
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


def load_results(results_dir: Path) -> dict[str, dict[str, str]]:
    results: dict[str, dict[str, str]] = {}
    if not results_dir.exists():
        return results
    for path in results_dir.glob("*.json"):
        try:
            data = json.loads(path.read_text())
        except json.JSONDecodeError:
            continue
        platform = str(data.get("platform") or path.stem)
        if platform not in PLATFORM_COLUMNS:
            continue
        platform_results: dict[str, str] = {}
        for version, status in (data.get("results") or {}).items():
            version = str(version).lstrip("v")
            if is_version(version):
                platform_results[version] = normalize_status(status)
        results[platform] = platform_results
    return results


def update_patch_version_file(path: Path, latest_version: str) -> bool:
    version = latest_version.strip()
    if not version:
        return False
    existing = path.read_text().strip() if path.exists() else ""
    if existing == version:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(version + "\n")
    return True


def update_test_version(path: Path, latest_version: str) -> bool:
    text = path.read_text()
    updated, count = re.subn(
        r'(const\s+defaultClaudePatchVersion\s*=\s*")([^"]+)(")',
        lambda match: f"{match.group(1)}{latest_version}{match.group(3)}",
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
        "--test-file",
        default="internal/cli/claude_patch_integration_test.go",
        help="Integration test file path",
    )
    parser.add_argument(
        "--patch-version-path",
        default="scripts/claude_patch_version.txt",
        help="Patch version file path for CI smoke tests",
    )
    parser.add_argument(
        "--results-dir",
        default="results",
        help="Directory with patch test result JSON files",
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
    results = load_results(Path(args.results_dir))
    for version in missing:
        version = str(version).lstrip("v")
        if not is_version(version):
            continue
        record = records.setdefault(version, {})
        record["tag"] = args.proxy_tag
        for platform in PLATFORM_COLUMNS:
            status = results.get(platform, {}).get(version)
            if status:
                record[platform] = status
            else:
                record[platform] = record.get(platform, "fail")

    table_updated = update_file(table_path, render_table(records))

    patch_updated = False
    test_updated = False
    if args.latest_version:
        patch_updated = update_patch_version_file(
            Path(args.patch_version_path), args.latest_version
        )
        test_updated = update_test_version(Path(args.test_file), args.latest_version)

    if table_updated or patch_updated or test_updated:
        print("updated")
    else:
        print("no changes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
