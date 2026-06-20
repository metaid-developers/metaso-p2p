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

list_manifest_namespaces() {
  local manifest_file="$1"
  local raw_namespaces_file="$TMP_ROOT/manifest-namespaces-raw.txt"
  local namespaces_file="$TMP_ROOT/manifest-namespaces.txt"
  local namespace=""

  awk '
    function fail(msg) {
      print msg > "/dev/stderr"
      status = 1
      exit 1
    }

    function hex_value(ch) {
      if (ch >= "0" && ch <= "9") {
        return ch + 0
      }
      ch = tolower(ch)
      if (ch >= "a" && ch <= "f") {
        return index("abcdef", ch) + 9
      }
      return -1
    }

    function hex_to_dec(hex,    i, value, digit, ch) {
      value = 0
      for (i = 1; i <= length(hex); i++) {
        ch = substr(hex, i, 1)
        digit = hex_value(ch)
        if (digit < 0) {
          return -1
        }
        value = (value * 16) + digit
      }
      return value
    }

    function decode_json_string(text,    i, ch, esc, hex, code, out) {
      out = ""
      text = substr(text, 2, length(text) - 2)
      for (i = 1; i <= length(text); i++) {
        ch = substr(text, i, 1)
        if (ch == "\"") {
          fail("includedNamespaces entries must be non-empty strings")
        }
        if (ch != "\\") {
          out = out ch
          continue
        }
        i++
        if (i > length(text)) {
          fail("includedNamespaces entries must be non-empty strings")
        }
        esc = substr(text, i, 1)
        if (esc == "\"" || esc == "\\" || esc == "/") {
          out = out esc
        } else if (esc == "b") {
          out = out sprintf("%c", 8)
        } else if (esc == "f") {
          out = out sprintf("%c", 12)
        } else if (esc == "n") {
          out = out sprintf("%c", 10)
        } else if (esc == "r") {
          out = out sprintf("%c", 13)
        } else if (esc == "t") {
          out = out sprintf("%c", 9)
        } else if (esc == "u") {
          hex = substr(text, i + 1, 4)
          if (length(hex) != 4) {
            fail("invalid unicode escape in includedNamespaces entry")
          }
          code = hex_to_dec(hex)
          if (code < 0) {
            fail("invalid unicode escape in includedNamespaces entry")
          }
          out = out sprintf("%c", code)
          i += 4
        } else {
          fail("invalid escape in includedNamespaces entry")
        }
      }
      return out
    }

    BEGIN {
      expecting_array = 0
      in_array = 0
      found = 0
      closed = 0
      count = 0
      status = 0
    }

    {
      line = $0
      if (closed) {
        next
      }

      if (!in_array) {
        if (line ~ /^[[:space:]]*"includedNamespaces"[[:space:]]*:[[:space:]]*\[[[:space:]]*$/) {
          in_array = 1
          found = 1
          next
        }
        if (line ~ /^[[:space:]]*"includedNamespaces"[[:space:]]*:[[:space:]]*$/) {
          expecting_array = 1
          next
        }
        if (expecting_array) {
          if (line !~ /^[[:space:]]*\[[[:space:]]*$/) {
            fail("includedNamespaces must be a non-empty list")
          }
          in_array = 1
          found = 1
          expecting_array = 0
        }
        next
      }

      if (line ~ /^[[:space:]]*\][[:space:]]*,?[[:space:]]*$/) {
        if (count == 0) {
          fail("includedNamespaces must be a non-empty list")
        }
        in_array = 0
        closed = 1
        next
      }

      raw = line
      sub(/^[[:space:]]*/, "", raw)
      sub(/[[:space:]]*$/, "", raw)
      if (substr(raw, length(raw), 1) == ",") {
        raw = substr(raw, 1, length(raw) - 1)
        sub(/[[:space:]]*$/, "", raw)
      }
      if (length(raw) < 2 || substr(raw, 1, 1) != "\"" || substr(raw, length(raw), 1) != "\"") {
        fail("includedNamespaces entries must be non-empty strings")
      }

      print decode_json_string(raw)
      count++
    }

    END {
      if (status) {
        exit status
      }
      if (expecting_array) {
        fail("includedNamespaces must be a non-empty list")
      }
      if (!found) {
        fail("includedNamespaces must be a non-empty list")
      }
      if (in_array || !closed) {
        fail("includedNamespaces array missing closing bracket")
      }
    }
  ' "$manifest_file" >"$raw_namespaces_file"

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

  list_manifest_namespaces "$manifest_file" >"$expected_namespaces_file"
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
