#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
TMP_ROOT=""

cleanup() {
  if [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]]; then
    rm -rf "${TMP_ROOT}"
  fi
}

trap cleanup EXIT

usage() {
  cat <<'EOF'
Usage: bootstrap-pack.sh --data-dir <dir> --output-dir <dir> --network <name> --source-node <label> [--include-cache]
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

json_escape() {
  printf '%s' "$1" | sed \
    -e 's/\\/\\\\/g' \
    -e 's/"/\\"/g'
}

require_arg() {
  local name="$1"
  local value="$2"
  [[ -n "$value" ]] || die "missing required argument: $name"
}

build_manifest() {
  local output_path="$1"
  shift
  local metaso_version="$1"
  local git_commit="$2"
  local built_at="$3"
  local network="$4"
  local source_node="$5"
  shift 5
  local namespaces=("$@")

  {
    printf '{\n'
    printf '  "schemaVersion": 1,\n'
    printf '  "metasoVersion": "%s",\n' "$(json_escape "$metaso_version")"
    printf '  "gitCommit": "%s",\n' "$(json_escape "$git_commit")"
    printf '  "builtAt": "%s",\n' "$(json_escape "$built_at")"
    printf '  "network": "%s",\n' "$(json_escape "$network")"
    printf '  "sourceNode": "%s",\n' "$(json_escape "$source_node")"
    printf '  "dataDirFormat": "pebble-per-namespace",\n'
    printf '  "includedNamespaces": [\n'
    local i
    for i in "${!namespaces[@]}"; do
      local suffix=","
      if [[ "$i" -eq $((${#namespaces[@]} - 1)) ]]; then
        suffix=""
      fi
      printf '    "%s"%s\n' "$(json_escape "${namespaces[$i]}")" "$suffix"
    done
    printf '  ]\n'
    printf '}\n'
  } >"$output_path"
}

main() {
  local data_dir=""
  local output_dir=""
  local network=""
  local source_node=""
  local include_cache=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --data-dir)
        [[ $# -ge 2 ]] || die "missing value for --data-dir"
        data_dir="$2"
        shift 2
        ;;
      --output-dir)
        [[ $# -ge 2 ]] || die "missing value for --output-dir"
        output_dir="$2"
        shift 2
        ;;
      --network)
        [[ $# -ge 2 ]] || die "missing value for --network"
        network="$2"
        shift 2
        ;;
      --source-node)
        [[ $# -ge 2 ]] || die "missing value for --source-node"
        source_node="$2"
        shift 2
        ;;
      --include-cache)
        include_cache=1
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

  require_arg --data-dir "$data_dir"
  require_arg --output-dir "$output_dir"
  require_arg --network "$network"
  require_arg --source-node "$source_node"
  [[ -d "$data_dir" ]] || die "data directory not found: $data_dir"
  mkdir -p "$output_dir"

  TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/metaso-bootstrap-pack.XXXXXX")
  local stage_dir="$TMP_ROOT/stage"
  mkdir -p "$stage_dir/namespaces"

  local namespaces=()
  local path=""
  while IFS= read -r path; do
    local namespace
    namespace=$(basename "$path")
    if [[ "$include_cache" -ne 1 && "$namespace" == cache_* ]]; then
      continue
    fi
    namespaces+=("$namespace")
  done < <(find "$data_dir" -mindepth 1 -maxdepth 1 -type d | sort)

  [[ "${#namespaces[@]}" -gt 0 ]] || die "no namespace directories selected from $data_dir"

  local namespace=""
  for namespace in "${namespaces[@]}"; do
    cp -R "$data_dir/$namespace" "$stage_dir/namespaces/$namespace"
  done

  local git_commit=""
  if git -C "$ROOT_DIR" rev-parse HEAD >/dev/null 2>&1; then
    git_commit=$(git -C "$ROOT_DIR" rev-parse HEAD)
  fi

  local metaso_version="dev"
  if git -C "$ROOT_DIR" rev-parse HEAD >/dev/null 2>&1; then
    metaso_version=$(git -C "$ROOT_DIR" rev-parse --short HEAD)
  fi

  local built_at
  built_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  build_manifest \
    "$stage_dir/manifest.json" \
    "$metaso_version" \
    "$git_commit" \
    "$built_at" \
    "$network" \
    "$source_node" \
    "${namespaces[@]}"

  : >"$stage_dir/checksums.txt"
  local file=""
  while IFS= read -r file; do
    local rel_path="${file#$stage_dir/}"
    printf '%s  %s\n' "$(sha256_file "$file")" "$rel_path" >>"$stage_dir/checksums.txt"
  done < <(
    {
      printf '%s\n' "$stage_dir/manifest.json"
      find "$stage_dir/namespaces" -type f | sort
    }
  )

  local timestamp
  timestamp=$(date -u +%Y%m%dT%H%M%SZ)
  local archive_path="$output_dir/metaso-p2p-bootstrap-$network-$timestamp.tar.gz"
  tar -czf "$archive_path" -C "$stage_dir" manifest.json checksums.txt namespaces

  printf '%s\n' "$archive_path"
}

main "$@"
