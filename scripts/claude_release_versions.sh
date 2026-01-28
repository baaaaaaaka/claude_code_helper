#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/claude_release_versions.sh [options]

List all available Claude Code versions from the release bucket.

Options:
  --json                 Output JSON array (default: newline list)
  --min-version VERSION  Minimum version to include (default: 2.1.19)
  --bucket-url URL       Override bucket URL (default: from claude_release_info.sh)
  -h, --help             Show help

Environment overrides:
  CLAUDE_MIN_VERSION
  CLAUDE_RELEASE_BUCKET_URL
EOF
}

json=0
min_version="${CLAUDE_MIN_VERSION:-2.1.19}"
bucket_url="${CLAUDE_RELEASE_BUCKET_URL:-}"
python_bin="${CLAUDE_PYTHON_BIN:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --json) json=1; shift ;;
    --min-version) min_version="$2"; shift 2 ;;
    --bucket-url) bucket_url="$2"; shift 2 ;;
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

if [ -z "$bucket_url" ]; then
  info="$(scripts/claude_release_info.sh --json)"
  bucket_url="$(
    printf "%s" "$info" | "$python_bin" - <<'PY'
import json
import sys

try:
    data = json.load(sys.stdin)
except json.JSONDecodeError:
    sys.exit(1)
print(data.get("gcs_bucket", ""))
PY
  )"
fi

if [ -z "$bucket_url" ]; then
  echo "Failed to resolve Claude release bucket URL." >&2
  exit 1
fi

"$python_bin" - "$bucket_url" "$min_version" "$json" <<'PY'
import json
import re
import sys
import urllib.parse
import urllib.request

bucket_url = sys.argv[1]
min_version = sys.argv[2].strip()
json_out = sys.argv[3] == "1"

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

bucket, prefix = split_bucket(bucket_url)
if not bucket:
    print("Invalid bucket URL", file=sys.stderr)
    sys.exit(1)

versions: set[str] = set()
page_token = ""
while True:
    params = {"delimiter": "/", "prefix": prefix}
    if page_token:
        params["pageToken"] = page_token
    url = "https://storage.googleapis.com/storage/v1/b/{}/o?{}".format(
        bucket, urllib.parse.urlencode(params)
    )
    try:
        with urllib.request.urlopen(url) as resp:
            data = json.load(resp)
    except Exception as exc:
        print(f"Failed to list bucket: {exc}", file=sys.stderr)
        sys.exit(1)
    for entry in data.get("prefixes", []):
        if prefix and not entry.startswith(prefix):
            continue
        version = entry[len(prefix):].strip("/")
        if version:
            versions.add(version)
    page_token = data.get("nextPageToken", "")
    if not page_token:
        break

filtered = [
    version
    for version in versions
    if is_version(version) and ge_min(version, min_version)
]
filtered.sort(key=version_key)

if json_out:
    print(json.dumps(filtered))
else:
    print("\n".join(filtered))
PY
