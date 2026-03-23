#!/usr/bin/env python3
import argparse
import json
import re
from pathlib import Path

PLATFORM_COLUMNS = [
    "linux",
    "mac",
    "windows",
    "centos7",
    "rockylinux8",
    "ubuntu20.04",
]


def version_key(version: str) -> list[int]:
    return [int(part) for part in version.split(".")]


def is_version(value: str) -> bool:
    return re.match(r"^[0-9]+(\.[0-9]+)+$", value) is not None


def normalize_status(value: str) -> str:
    status = str(value).strip().lower()
    if status == "pass":
        return "pass"
    if status == "missing":
        return "missing"
    return "fail"


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


def parse_job_statuses(values: list[str]) -> dict[str, str]:
    statuses: dict[str, str] = {}
    for value in values:
        name, sep, status = value.partition("=")
        if not sep:
            raise SystemExit(f"Invalid --job-status value: {value}")
        normalized = status.strip().lower()
        if not normalized:
            continue
        statuses[name.strip()] = normalized
    return statuses


def collect_failures(
    missing_versions: list[str], results: dict[str, dict[str, str]]
) -> dict[str, dict[str, str]]:
    failures: dict[str, dict[str, str]] = {}
    for raw_version in missing_versions:
        version = str(raw_version).lstrip("v")
        if not is_version(version):
            continue
        row: dict[str, str] = {}
        for platform in PLATFORM_COLUMNS:
            status = results.get(platform, {}).get(version, "missing")
            if status != "pass":
                row[platform] = status
        if row:
            failures[version] = row
    return failures


def collect_failed_jobs(job_statuses: dict[str, str]) -> dict[str, str]:
    failed: dict[str, str] = {}
    for name, status in job_statuses.items():
        if status not in {"success", "skipped"}:
            failed[name] = status
    return failed


def build_title(failures: dict[str, dict[str, str]], proxy_tag: str) -> str:
    if not failures:
        return f"Claude Code monitor workflow failure on {proxy_tag}"
    versions = sorted(failures, key=version_key)
    if len(versions) == 1:
        return f"Claude Code monitor failure: {versions[0]} on {proxy_tag}"
    preview = ", ".join(versions[:3])
    if len(versions) > 3:
        preview = f"{preview} +{len(versions) - 3} more"
    return f"Claude Code monitor failures: {preview} on {proxy_tag}"


def build_body(
    failures: dict[str, dict[str, str]],
    failed_jobs: dict[str, str],
    proxy_tag: str,
    workflow_run_url: str,
) -> str:
    lines = [
        "# Claude Code monitor failure",
        "",
        "The scheduled Claude Code monitor detected a problem while validating patch compatibility for new Claude Code releases.",
        "",
        f"- claude-proxy tag: `{proxy_tag}`",
        f"- Workflow run: {workflow_run_url or 'n/a'}",
        "",
    ]
    if failures:
        lines.extend(
            [
                "## Version results",
                "",
                "| Claude Code version | linux | mac | windows | centos7 | rockylinux8 | ubuntu20.04 |",
                "| --- | --- | --- | --- | --- | --- | --- |",
            ]
        )
        for version in sorted(failures, key=version_key):
            row = failures[version]
            lines.append(
                "| "
                + " | ".join(
                    [
                        version,
                        *(row.get(platform, "pass") for platform in PLATFORM_COLUMNS),
                    ]
                )
                + " |"
            )
    else:
        lines.extend(
            [
                "## Version results",
                "",
                "No failing per-version result rows were captured before the workflow failed.",
            ]
        )

    if failed_jobs:
        lines.extend(["", "## Failed jobs", ""])
        for name in sorted(failed_jobs):
            lines.append(f"- `{name}`: `{failed_jobs[name]}`")

    lines.extend(
        [
            "",
            "This issue was created automatically by `.github/workflows/claude-code-monitor.yml`.",
        ]
    )
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Create GitHub issue payloads for Claude Code monitor failures."
    )
    parser.add_argument("--missing-json", default="[]", help="JSON array of versions")
    parser.add_argument("--proxy-tag", default="", help="claude-proxy tag")
    parser.add_argument(
        "--results-dir",
        default="results",
        help="Directory with patch test result JSON files",
    )
    parser.add_argument(
        "--workflow-run-url",
        default="",
        help="Workflow run URL for the monitor execution",
    )
    parser.add_argument(
        "--job-status",
        action="append",
        default=[],
        help="Job status in the form name=status",
    )
    parser.add_argument(
        "--output-json",
        default="",
        help="Optional path to write the computed JSON payload",
    )
    parser.add_argument(
        "--output-body",
        default="",
        help="Optional path to write the issue body markdown",
    )
    args = parser.parse_args()

    try:
        missing = json.loads(args.missing_json) if args.missing_json else []
    except json.JSONDecodeError as exc:
        raise SystemExit(f"Invalid JSON for --missing-json: {exc}") from exc

    if not isinstance(missing, list):
        raise SystemExit("--missing-json must decode to a JSON array")

    if not args.proxy_tag:
        raise SystemExit("--proxy-tag is required")

    results = load_results(Path(args.results_dir))
    failures = collect_failures(missing, results)
    failed_jobs = collect_failed_jobs(parse_job_statuses(args.job_status))

    payload: dict[str, object] = {"should_create": bool(failures or failed_jobs)}
    if failures or failed_jobs:
        payload["title"] = build_title(failures, args.proxy_tag)
        body = build_body(failures, failed_jobs, args.proxy_tag, args.workflow_run_url)
        payload["body"] = body
        if args.output_body:
            output_body_path = Path(args.output_body)
            output_body_path.parent.mkdir(parents=True, exist_ok=True)
            output_body_path.write_text(body)
            payload["body_path"] = str(output_body_path)
    elif args.output_body:
        output_body_path = Path(args.output_body)
        if output_body_path.exists():
            output_body_path.unlink()

    if args.output_json:
        output_json_path = Path(args.output_json)
        output_json_path.parent.mkdir(parents=True, exist_ok=True)
        output_json_path.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    else:
        print(json.dumps(payload))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
