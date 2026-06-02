#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/claude_release_versions.sh [options]

List all available Claude Code versions from the release bucket.
Falls back to the npm registry when bucket listing is unavailable.

Options:
  --json                 Output JSON array (default: newline list)
  --min-version VERSION  Minimum version to include (default: 2.1.19)
  --bucket-url URL       Override bucket URL (default: from claude_release_info.sh)
  --npm-registry-url URL Override npm registry URL
  --release-bucket-url URL
                         Release download base URL for manifest validation
  --source SOURCE        Version source: auto, bucket, or npm (default: auto)
  -h, --help             Show help

Environment overrides:
  CLAUDE_MIN_VERSION
  CLAUDE_RELEASE_BUCKET_URL
  CLAUDE_NPM_REGISTRY_URL
  CLAUDE_RELEASE_DOWNLOAD_BASE_URL
  CLAUDE_RELEASE_VERSION_SOURCE
EOF
}

json=0
min_version="${CLAUDE_MIN_VERSION:-2.1.19}"
bucket_url="${CLAUDE_RELEASE_BUCKET_URL:-}"
npm_registry_url="${CLAUDE_NPM_REGISTRY_URL:-https://registry.npmjs.org/@anthropic-ai%2fclaude-code}"
release_bucket_url="${CLAUDE_RELEASE_DOWNLOAD_BASE_URL:-}"
version_source="${CLAUDE_RELEASE_VERSION_SOURCE:-auto}"
python_bin="${CLAUDE_PYTHON_BIN:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --json) json=1; shift ;;
    --min-version) min_version="$2"; shift 2 ;;
    --bucket-url) bucket_url="$2"; shift 2 ;;
    --npm-registry-url) npm_registry_url="$2"; shift 2 ;;
    --release-bucket-url) release_bucket_url="$2"; shift 2 ;;
    --source) version_source="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "Unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -z "$python_bin" ]; then
  if command -v python3 >/dev/null 2>&1; then
    python_bin="python3"
  elif command -v python >/dev/null 2>&1; then
    python_bin="python"
  else
    echo "Need python3 or python to list versions" >&2
    exit 1
  fi
fi

if [ -z "$bucket_url" ] && [ "$version_source" != "npm" ]; then
  info="$(scripts/claude_release_info.sh --json)"
  resolved_urls="$(
    CLAUDE_RELEASE_INFO_JSON="$info" "$python_bin" - <<'PY'
import json
import os
import sys

try:
    data = json.loads(os.environ["CLAUDE_RELEASE_INFO_JSON"])
except json.JSONDecodeError:
    sys.exit(1)
print(data.get("gcs_bucket") or data.get("release_bucket", ""))
print(data.get("release_bucket", ""))
PY
  )"
  bucket_url="$(printf "%s\n" "$resolved_urls" | sed -n '1p')"
  if [ -z "$release_bucket_url" ]; then
    release_bucket_url="$(printf "%s\n" "$resolved_urls" | sed -n '2p')"
  fi
fi

if [ -z "$bucket_url" ] && [ "$version_source" != "npm" ]; then
  echo "Failed to resolve Claude release bucket URL." >&2
  exit 1
fi

"$python_bin" - "$bucket_url" "$min_version" "$json" "$version_source" "$npm_registry_url" "$release_bucket_url" <<'PY'
from concurrent.futures import ThreadPoolExecutor
import json
import re
import sys
import urllib.error
import urllib.parse
import urllib.request

bucket_url = sys.argv[1]
min_version = sys.argv[2].strip()
json_out = sys.argv[3] == "1"
version_source = sys.argv[4]
npm_registry_url = sys.argv[5]
release_bucket_url = sys.argv[6].rstrip("/")

VALID_SOURCES = {"auto", "bucket", "npm"}

if version_source not in VALID_SOURCES:
    print(f"Unknown version source: {version_source}", file=sys.stderr)
    sys.exit(2)

class SourceError(Exception):
    pass

def split_bucket(url: str) -> tuple[str, str]:
    if url.startswith("https://storage.googleapis.com/"):
        path = url[len("https://storage.googleapis.com/"):]
    elif url.startswith("gs://"):
        path = url[len("gs://"):]
    else:
        path = url.lstrip("/")
    bucket, _, prefix = path.partition("/")
    prefix = prefix.strip("/")
    if prefix:
        prefix = prefix + "/"
    return bucket, prefix

def version_key(version: str) -> list[int]:
    return [int(part) for part in version.split(".")]

def is_version(version: str) -> bool:
    return re.match(r"^[0-9]+(\.[0-9]+)+$", version) is not None

def ge_min(version: str, minimum: str) -> bool:
    if not minimum:
        return True
    parts = [int(part) for part in version.split(".")]
    min_parts = [int(part) for part in minimum.split(".")]
    max_len = max(len(parts), len(min_parts))
    parts += [0] * (max_len - len(parts))
    min_parts += [0] * (max_len - len(min_parts))
    return parts >= min_parts

def read_json(url: str) -> object:
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/json",
            "User-Agent": "claude-code-helper-version-monitor",
        },
    )
    with urllib.request.urlopen(request, timeout=20) as resp:
        return json.load(resp)

def list_bucket_versions(url: str) -> set[str]:
    bucket, prefix = split_bucket(url)
    if not bucket:
        raise SourceError("Invalid bucket URL")

    versions: set[str] = set()
    page_token = ""
    while True:
        params = {"delimiter": "/", "prefix": prefix}
        if page_token:
            params["pageToken"] = page_token
        list_url = "https://storage.googleapis.com/storage/v1/b/{}/o?{}".format(
            bucket, urllib.parse.urlencode(params)
        )
        try:
            data = read_json(list_url)
        except Exception as exc:
            raise SourceError(str(exc)) from exc
        for entry in data.get("prefixes", []):
            if prefix and not entry.startswith(prefix):
                continue
            version = entry[len(prefix):].strip("/")
            if version:
                versions.add(version)
        page_token = data.get("nextPageToken", "")
        if not page_token:
            break
    return versions

def list_npm_versions(url: str) -> set[str]:
    try:
        data = read_json(url)
    except Exception as exc:
        raise SourceError(str(exc)) from exc
    if not isinstance(data, dict) or not isinstance(data.get("versions"), dict):
        raise SourceError("registry payload missing versions object")
    return set(data["versions"].keys())

def has_release_manifest(version: str) -> bool:
    if not release_bucket_url:
        return True
    url = f"{release_bucket_url}/{urllib.parse.quote(version)}/manifest.json"
    request = urllib.request.Request(
        url,
        method="HEAD",
        headers={"User-Agent": "claude-code-helper-version-monitor"},
    )
    try:
        with urllib.request.urlopen(request, timeout=20) as resp:
            status = getattr(resp, "status", resp.getcode())
            return 200 <= status < 400
    except urllib.error.HTTPError as exc:
        if exc.code == 404:
            return False
        print(
            f"Warning: failed to verify release manifest for {version}: HTTP {exc.code}",
            file=sys.stderr,
        )
        return True
    except Exception as exc:
        print(
            f"Warning: failed to verify release manifest for {version}: {exc}",
            file=sys.stderr,
        )
        return True

versions: set[str] = set()
used_source = ""
if version_source != "npm":
    try:
        versions = list_bucket_versions(bucket_url)
        used_source = "bucket"
    except SourceError as exc:
        print(f"Failed to list bucket: {exc}", file=sys.stderr)
        if version_source == "bucket":
            sys.exit(1)
        print(f"Falling back to npm registry: {npm_registry_url}", file=sys.stderr)

if not versions and version_source != "bucket":
    try:
        versions = list_npm_versions(npm_registry_url)
        used_source = "npm"
    except SourceError as exc:
        print(f"Failed to list npm registry: {exc}", file=sys.stderr)
        sys.exit(1)

filtered = [
    version
    for version in versions
    if is_version(version) and ge_min(version, min_version)
]
filtered.sort(key=version_key)

if used_source == "npm" and release_bucket_url and filtered:
    workers = min(16, len(filtered))
    with ThreadPoolExecutor(max_workers=workers) as executor:
        manifest_results = list(executor.map(has_release_manifest, filtered))
    skipped = [
        version
        for version, has_manifest in zip(filtered, manifest_results)
        if not has_manifest
    ]
    if skipped:
        print(
            "Skipping npm versions without release manifests: " + ", ".join(skipped),
            file=sys.stderr,
        )
    filtered = [
        version
        for version, has_manifest in zip(filtered, manifest_results)
        if has_manifest
    ]

if json_out:
    print(json.dumps(filtered))
else:
    print("\n".join(filtered))
PY
