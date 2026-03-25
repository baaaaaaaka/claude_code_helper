#!/usr/bin/env python3
import argparse
import json
from pathlib import Path

MONITORED_FIELDS = [
    ("install_sh_url", "install.sh URL"),
    ("install_ps1_url", "install.ps1 URL"),
    ("install_cmd_url", "install.cmd URL"),
    ("gcs_bucket", "release bucket"),
]

SUCCESS_STATES = {"success", "skipped"}


def load_json(path_value: str, description: str) -> dict[str, object]:
    path = Path(path_value)
    try:
        data = json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise SystemExit(f"{description} file not found: {path}") from exc
    except json.JSONDecodeError as exc:
        raise SystemExit(f"Invalid JSON in {path}: {exc}") from exc
    if not isinstance(data, dict):
        raise SystemExit(f"{description} must decode to a JSON object: {path}")
    return data


def normalize_status(value: str) -> str:
    status = str(value).strip().lower()
    return status or "unknown"


def compare_entries(
    baseline: dict[str, object], current: dict[str, object]
) -> list[dict[str, str]]:
    changes: list[dict[str, str]] = []
    for key, label in MONITORED_FIELDS:
        baseline_value = str(baseline.get(key, "") or "").strip()
        current_value = str(current.get(key, "") or "").strip()
        if baseline_value != current_value:
            changes.append(
                {
                    "key": key,
                    "label": label,
                    "baseline": baseline_value,
                    "current": current_value,
                }
            )
    return changes


def build_title(
    changes: list[dict[str, str]], resolve_status: str, smoke_status: str
) -> str:
    if changes:
        if len(changes) == 1:
            return f"Claude Code install entry changed: {changes[0]['key']}"
        return f"Claude Code install entries changed ({len(changes)})"
    if resolve_status not in SUCCESS_STATES:
        return "Claude Code install monitor failed to resolve release metadata"
    return "Claude Code install smoke test failed"


def build_body(
    baseline_path: str,
    baseline: dict[str, object],
    current: dict[str, object],
    changes: list[dict[str, str]],
    workflow_run_url: str,
    resolve_status: str,
    smoke_status: str,
) -> str:
    lines = [
        "# Claude Code install monitor",
        "",
        "The scheduled Claude Code install monitor detected a change in the official install entrypoints or the install smoke test failed.",
        "",
        f"- Baseline file: `{baseline_path}`",
        f"- Workflow run: {workflow_run_url or 'n/a'}",
        f"- Release metadata: `{resolve_status}`",
        f"- Install smoke: `{smoke_status}`",
        "",
    ]

    if current:
        latest_version = str(current.get("latest_version", "") or "").strip()
        latest_manifest_url = str(current.get("latest_manifest_url", "") or "").strip()
        if latest_version:
            lines.append(f"- Latest version seen: `{latest_version}`")
        if latest_manifest_url:
            lines.append(f"- Latest manifest URL: `{latest_manifest_url}`")
        lines.append("")
        lines.extend(
            [
                "## Entrypoint snapshot",
                "",
                "| Field | Baseline | Current |",
                "| --- | --- | --- |",
            ]
        )
        for key, label in MONITORED_FIELDS:
            baseline_value = str(baseline.get(key, "") or "").strip() or "(empty)"
            current_value = str(current.get(key, "") or "").strip() or "(empty)"
            lines.append(f"| {label} | `{baseline_value}` | `{current_value}` |")
    else:
        lines.extend(
            [
                "## Entrypoint snapshot",
                "",
                "Current release metadata was not captured because the discovery step failed.",
            ]
        )

    if changes:
        lines.extend(["", "## Detected changes", ""])
        for change in changes:
            lines.append(
                f"- `{change['key']}`: `{change['baseline'] or '(empty)'}` -> `{change['current'] or '(empty)'}`"
            )
    elif resolve_status in SUCCESS_STATES and smoke_status not in SUCCESS_STATES:
        lines.extend(
            [
                "",
                "## Smoke failure",
                "",
                "The official install metadata still matches the baseline, but the real install+launch smoke test failed.",
            ]
        )
    elif resolve_status not in SUCCESS_STATES:
        lines.extend(
            [
                "",
                "## Metadata failure",
                "",
                "The workflow could not resolve the current install release metadata from the official installer endpoints.",
            ]
        )

    lines.extend(
        [
            "",
            "This issue was created automatically by `.github/workflows/claude-code-install-monitor.yml`.",
        ]
    )
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Create GitHub issue payloads for Claude Code install monitor results."
    )
    parser.add_argument(
        "--baseline-path",
        required=True,
        help="Path to the committed install entrypoint baseline JSON",
    )
    parser.add_argument(
        "--current-path",
        default="",
        help="Optional path to the current install release info JSON",
    )
    parser.add_argument(
        "--workflow-run-url",
        default="",
        help="Workflow run URL for the current execution",
    )
    parser.add_argument(
        "--resolve-status",
        default="unknown",
        help="GitHub Actions step outcome for release metadata resolution",
    )
    parser.add_argument(
        "--smoke-status",
        default="unknown",
        help="GitHub Actions step outcome for the install smoke test",
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

    baseline = load_json(args.baseline_path, "Baseline")
    current = load_json(args.current_path, "Current release info") if args.current_path else {}

    resolve_status = normalize_status(args.resolve_status)
    smoke_status = normalize_status(args.smoke_status)
    changes = compare_entries(baseline, current) if current else []

    should_fail = bool(
        changes
        or resolve_status not in SUCCESS_STATES
        or smoke_status not in SUCCESS_STATES
    )

    payload: dict[str, object] = {
        "changes": changes,
        "should_create": should_fail,
        "should_fail": should_fail,
    }

    if should_fail:
        payload["title"] = build_title(changes, resolve_status, smoke_status)
        body = build_body(
            args.baseline_path,
            baseline,
            current,
            changes,
            args.workflow_run_url,
            resolve_status,
            smoke_status,
        )
        payload["body"] = body
        if args.output_body:
            output_body_path = Path(args.output_body)
            output_body_path.parent.mkdir(parents=True, exist_ok=True)
            output_body_path.write_text(body, encoding="utf-8")
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
