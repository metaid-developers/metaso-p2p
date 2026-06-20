#!/usr/bin/env bash

set -euo pipefail

PYTHON_BIN=""
TMP_ROOT=""

cleanup() {
  if [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]]; then
    rm -rf "${TMP_ROOT}"
  fi
}

trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: bootstrap-restore.sh --archive <file> --target-dir <dir> [--force]
EOF
}

die() {
  echo "error: $*" >&2
  exit 1
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

require_python3() {
  if [[ -n "$PYTHON_BIN" ]]; then
    return
  fi
  if [[ -x /usr/bin/python3 ]]; then
    PYTHON_BIN=/usr/bin/python3
    return
  fi
  if have_cmd python3; then
    PYTHON_BIN=$(command -v python3)
    return
  fi
  die "missing python3 (required for manifest.json validation)"
}

sha256_file() {
  local path="$1"
  if have_cmd shasum; then
    shasum -a 256 "$path" | awk '{print $1}'
    return
  fi
  if have_cmd sha256sum; then
    sha256sum "$path" | awk '{print $1}'
    return
  fi
  die "missing shasum or sha256sum"
}

dir_non_empty() {
  local dir="$1"
  [[ -d "$dir" ]] || return 1
  find "$dir" -mindepth 1 -maxdepth 1 -print -quit | grep -q .
}

validate_manifest_and_list_namespaces() {
  local manifest_file="$1"
  local raw_namespaces_file="$TMP_ROOT/manifest-namespaces-raw.txt"
  local namespaces_file="$TMP_ROOT/manifest-namespaces.txt"
  local namespace=""

  "$PYTHON_BIN" - "$manifest_file" >"$raw_namespaces_file" <<'PY'
import json
import re
import sys
from datetime import datetime

path = sys.argv[1]

try:
    with open(path, "r", encoding="utf-8") as handle:
        manifest = json.load(handle)
except json.JSONDecodeError as exc:
    raise SystemExit(f"invalid manifest.json: {exc.msg}")

if not isinstance(manifest, dict):
    raise SystemExit("manifest.json must be a JSON object")

required_fields = (
    "schemaVersion",
    "metasoVersion",
    "gitCommit",
    "builtAt",
    "network",
    "sourceNode",
    "dataDirFormat",
    "includedNamespaces",
)

for field in required_fields:
    if field not in manifest:
        raise SystemExit(f"missing required manifest field: {field}")

schema_version = manifest["schemaVersion"]
if type(schema_version) is not int:
    raise SystemExit("manifest field schemaVersion must be an integer")
if schema_version != 1:
    raise SystemExit(f"unsupported manifest schemaVersion: {schema_version}")

for field in ("metasoVersion", "network", "sourceNode"):
    value = manifest[field]
    if not isinstance(value, str) or value == "":
        raise SystemExit(f"manifest field {field} must be a non-empty string")

git_commit = manifest["gitCommit"]
if not isinstance(git_commit, str):
    raise SystemExit("manifest field gitCommit must be a string")
if git_commit != "" and re.fullmatch(r"[0-9a-fA-F]{40}", git_commit) is None:
    raise SystemExit("manifest field gitCommit must be empty or a 40-character hex SHA")

built_at = manifest["builtAt"]
if not isinstance(built_at, str) or built_at == "":
    raise SystemExit("manifest field builtAt must be a non-empty string")
if re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", built_at) is None:
    raise SystemExit("manifest field builtAt must be a UTC RFC3339 timestamp like 2026-06-20T12:34:56Z")
try:
    datetime.strptime(built_at, "%Y-%m-%dT%H:%M:%SZ")
except ValueError as exc:
    raise SystemExit("manifest field builtAt must be a UTC RFC3339 timestamp like 2026-06-20T12:34:56Z") from exc

data_dir_format = manifest["dataDirFormat"]
if not isinstance(data_dir_format, str):
    raise SystemExit("manifest field dataDirFormat must be a string")
if data_dir_format != "pebble-per-namespace":
    raise SystemExit(f"unsupported manifest dataDirFormat: {data_dir_format}")

included_namespaces = manifest["includedNamespaces"]
if not isinstance(included_namespaces, list) or len(included_namespaces) == 0:
    raise SystemExit("manifest field includedNamespaces must be a non-empty list")

for namespace in included_namespaces:
    if not isinstance(namespace, str) or namespace == "":
        raise SystemExit("manifest field includedNamespaces entries must be non-empty strings")
    print(namespace)
PY

  : >"$namespaces_file"
  while IFS= read -r namespace || [[ -n "$namespace" ]]; do
    [[ -n "$namespace" ]] || die "includedNamespaces entries must be non-empty strings"
    [[ "$namespace" != */* ]] || die "invalid namespace entry: $namespace"
    [[ "$namespace" != "." && "$namespace" != ".." ]] || die "invalid namespace entry: $namespace"
    grep -Fqx "$namespace" "$namespaces_file" && die "duplicate namespace entry: $namespace"
    printf '%s\n' "$namespace" >>"$namespaces_file"
  done <"$raw_namespaces_file"

  [[ -s "$namespaces_file" ]] || die "includedNamespaces must be a non-empty list"
  cat "$namespaces_file"
}

require_archive_root_layout() {
  local root="$1"
  local entry=""
  local name=""

  while IFS= read -r entry; do
    name=$(basename "$entry")
    case "$name" in
      manifest.json|checksums.txt|namespaces)
        ;;
      *)
        die "unexpected archive root entry: $name"
        ;;
    esac
  done < <(find "$root" -mindepth 1 -maxdepth 1 -print | sort)
}

require_tree_without_symlinks() {
  local root="$1"
  local label="$2"
  local symlink_path=""

  symlink_path=$(find "$root" -type l -print -quit)
  if [[ -n "$symlink_path" ]]; then
    die "$label contains symlink: $symlink_path"
  fi
}

verify_checksums() {
  local manifest_file="$1"
  local checksums_file="$2"
  local root_dir="$3"
  local expected_namespaces_file="$TMP_ROOT/expected-namespaces.txt"
  local expected_payloads_file="$TMP_ROOT/expected-payloads.txt"
  local expected_dirs_file="$TMP_ROOT/expected-directories.txt"
  local line=""
  local rel_dir=""
  local payload_path=""
  local actual_dir=""
  local expected_dir=""
  local manifest_checksum=""
  local manifest_entry_count=0

  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -n "$line" ]] || continue
    local expected="${line%%  *}"
    local rel_path="${line#*  }"
    [[ "$expected" != "$line" ]] || die "invalid checksum entry: $line"
    if [[ "$rel_path" == "manifest.json" ]]; then
      manifest_entry_count=$((manifest_entry_count + 1))
      [[ "$manifest_entry_count" -eq 1 ]] || die "duplicate checksum entry for manifest.json"
      manifest_checksum="$expected"
    fi
  done <"$checksums_file"

  [[ -n "$manifest_checksum" ]] || die "manifest.json missing from checksums.txt"
  [[ "$(sha256_file "$manifest_file")" == "$manifest_checksum" ]] || \
    die "checksum mismatch for manifest.json"

  validate_manifest_and_list_namespaces "$manifest_file" >"$expected_namespaces_file"
  : >"$expected_payloads_file"
  printf 'namespaces\n' >"$expected_dirs_file"
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -n "$line" ]] || continue
    printf 'namespaces/%s\n' "$line" >>"$expected_dirs_file"
  done <"$expected_namespaces_file"

  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -n "$line" ]] || continue
    local expected="${line%%  *}"
    local rel_path="${line#*  }"
    [[ "$expected" != "$line" ]] || die "invalid checksum entry: $line"
    local path="$root_dir/$rel_path"
    [[ -f "$path" ]] || die "checksum target missing: $rel_path"
    local actual
    actual=$(sha256_file "$path")
    [[ "$actual" == "$expected" ]] || die "checksum mismatch for $rel_path"
    if [[ "$rel_path" == namespaces/* ]]; then
      local rel_namespace_path="${rel_path#namespaces/}"
      local top_namespace="${rel_namespace_path%%/*}"
      grep -Fqx "$top_namespace" "$expected_namespaces_file" || \
        die "payload namespace not declared in manifest.json: $top_namespace"
      printf '%s\n' "$rel_path" >>"$expected_payloads_file"
      rel_dir=$(dirname "$rel_path")
      while [[ "$rel_dir" != "." ]]; do
        printf '%s\n' "$rel_dir" >>"$expected_dirs_file"
        [[ "$rel_dir" == "namespaces" ]] && break
        rel_dir=$(dirname "$rel_dir")
      done
    fi
  done <"$checksums_file"

  sort -u "$expected_payloads_file" -o "$expected_payloads_file"
  sort -u "$expected_dirs_file" -o "$expected_dirs_file"

  while IFS= read -r payload_path; do
    local rel_payload_path="${payload_path#$root_dir/}"
    grep -Fqx "$rel_payload_path" "$expected_payloads_file" || \
      die "payload file not listed in checksums.txt: $rel_payload_path"
  done < <(find "$root_dir/namespaces" -type f | sort)

  while IFS= read -r actual_dir; do
    local rel_actual_dir="${actual_dir#$root_dir/}"
    grep -Fqx "$rel_actual_dir" "$expected_dirs_file" || \
      die "unexpected directory in archive payload: $rel_actual_dir"
  done < <(find "$root_dir/namespaces" -type d | sort)

  while IFS= read -r expected_dir || [[ -n "$expected_dir" ]]; do
    [[ -d "$root_dir/$expected_dir" ]] || \
      die "expected directory missing from archive payload: $expected_dir"
  done <"$expected_dirs_file"
}

main() {
  local archive=""
  local target_dir=""
  local force=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --archive)
        [[ $# -ge 2 ]] || die "missing value for --archive"
        archive="$2"
        shift 2
        ;;
      --target-dir)
        [[ $# -ge 2 ]] || die "missing value for --target-dir"
        target_dir="$2"
        shift 2
        ;;
      --force)
        force=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1"
        ;;
    esac
  done

  [[ -n "$archive" ]] || die "missing required argument: --archive"
  [[ -n "$target_dir" ]] || die "missing required argument: --target-dir"
  [[ -f "$archive" ]] || die "archive not found: $archive"
  if [[ -L "$target_dir" ]]; then
    die "target path must be a real directory path, not a symlink: $target_dir"
  fi
  if [[ -e "$target_dir" && ! -d "$target_dir" ]]; then
    die "target path is not a directory: $target_dir"
  fi

  TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/metaso-bootstrap-restore.XXXXXX")
  local unpack_dir="$TMP_ROOT/unpack"
  mkdir -p "$unpack_dir"
  tar -xzf "$archive" -C "$unpack_dir"

  [[ -f "$unpack_dir/manifest.json" ]] || die "archive missing manifest.json"
  [[ -f "$unpack_dir/checksums.txt" ]] || die "archive missing checksums.txt"
  [[ -d "$unpack_dir/namespaces" ]] || die "archive missing namespaces/"

  require_tree_without_symlinks "$unpack_dir" "archive payload"
  require_archive_root_layout "$unpack_dir"
  require_python3
  verify_checksums "$unpack_dir/manifest.json" "$unpack_dir/checksums.txt" "$unpack_dir"

  local backup_dir=""
  if dir_non_empty "$target_dir"; then
    if [[ "$force" -ne 1 ]]; then
      die "target directory is non-empty; use --force to replace it"
    fi
    local parent_dir
    parent_dir=$(dirname "$target_dir")
    local base_name
    base_name=$(basename "$target_dir")
    backup_dir="$parent_dir/$base_name.backup-$(date -u +%Y%m%dT%H%M%SZ)"
    mv "$target_dir" "$backup_dir"
  fi

  mkdir -p "$target_dir"
  cp -R "$unpack_dir/namespaces/." "$target_dir/"

  if [[ -n "$backup_dir" ]]; then
    printf 'backup: %s\n' "$backup_dir"
  fi
  printf 'restored: %s\n' "$target_dir"
}

main "$@"
