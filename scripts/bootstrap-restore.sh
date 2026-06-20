#!/usr/bin/env bash

set -euo pipefail

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
  local checksums_file="$1"
  local root_dir="$2"
  local line=""
  local expected_payloads_file="$TMP_ROOT/expected-payloads.txt"
  : >"$expected_payloads_file"
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
      printf '%s\n' "$rel_path" >>"$expected_payloads_file"
    fi
  done <"$checksums_file"

  local payload_path=""
  while IFS= read -r payload_path; do
    local rel_payload_path="${payload_path#$root_dir/}"
    grep -Fqx "$rel_payload_path" "$expected_payloads_file" || \
      die "payload file not listed in checksums.txt: $rel_payload_path"
  done < <(find "$root_dir/namespaces" -type f | sort)
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
  verify_checksums "$unpack_dir/checksums.txt" "$unpack_dir"

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
