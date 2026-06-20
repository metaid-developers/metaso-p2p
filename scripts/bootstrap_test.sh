#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
PACK_SCRIPT="$ROOT_DIR/scripts/bootstrap-pack.sh"
RESTORE_SCRIPT="$ROOT_DIR/scripts/bootstrap-restore.sh"
PYTHON_BIN=""
TMP_ROOT=""

cleanup() {
  if [[ -n "${TMP_ROOT}" && -d "${TMP_ROOT}" ]]; then
    rm -rf "${TMP_ROOT}"
  fi
}

trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

pass() {
  echo "PASS: $*"
}

assert_file_exists() {
  local path="$1"
  [[ -e "$path" ]] || fail "expected file to exist: $path"
}

assert_contains() {
  local file="$1"
  local pattern="$2"
  grep -Fq "$pattern" "$file" || fail "expected '$pattern' in $file"
}

assert_not_contains() {
  local file="$1"
  local pattern="$2"
  if grep -Fq "$pattern" "$file"; then
    fail "did not expect '$pattern' in $file"
  fi
}

assert_glob_count() {
  local dir="$1"
  local glob="$2"
  local expected="$3"
  shopt -s nullglob
  local matches=("$dir"/$glob)
  shopt -u nullglob
  local actual="${#matches[@]}"
  [[ "$actual" == "$expected" ]] || fail "expected $expected matches for $dir/$glob, got $actual"
}

assert_json_string_value() {
  local file="$1"
  local key="$2"
  local expected="$3"
  "$PYTHON_BIN" - "$file" "$key" "$expected" <<'PY'
import json
import sys

path, key, expected = sys.argv[1:4]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

actual = data.get(key)
if actual != expected:
    raise SystemExit(f"expected {key}={expected!r}, got {actual!r}")
PY
}

assert_json_array_contains() {
  local file="$1"
  local key="$2"
  local expected="$3"
  "$PYTHON_BIN" - "$file" "$key" "$expected" <<'PY'
import json
import sys

path, key, expected = sys.argv[1:4]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

values = data.get(key)
if not isinstance(values, list):
    raise SystemExit(f"expected {key} to be a list, got {type(values).__name__}")
if expected not in values:
    raise SystemExit(f"expected {expected!r} in {key}, got {values!r}")
PY
}

run_expect_fail() {
  local label="$1"
  shift
  set +e
  "$@" >"$TMP_ROOT/${label}.stdout" 2>"$TMP_ROOT/${label}.stderr"
  local status=$?
  set -e
  [[ "$status" -ne 0 ]] || fail "expected command to fail for $label"
}

require_scripts_present() {
  [[ -x "$PACK_SCRIPT" ]] || fail "missing executable pack script: $PACK_SCRIPT"
  [[ -x "$RESTORE_SCRIPT" ]] || fail "missing executable restore script: $RESTORE_SCRIPT"
  if command -v /usr/bin/python3 >/dev/null 2>&1; then
    PYTHON_BIN=/usr/bin/python3
    return
  fi
  if command -v python3 >/dev/null 2>&1; then
    PYTHON_BIN=$(command -v python3)
    return
  fi
  fail "missing python3"
}

extract_archive() {
  local archive="$1"
  local destination="$2"
  mkdir -p "$destination"
  tar -xzf "$archive" -C "$destination"
}

create_fixture_data() {
  local data_dir="$1"
  mkdir -p \
    "$data_dir/indexer_meta" \
    "$data_dir/social" \
    "$data_dir/cache_social"
  printf 'meta:100\n' >"$data_dir/indexer_meta/000001.sst"
  printf 'social:200\n' >"$data_dir/social/MANIFEST-000001"
  printf 'cache:300\n' >"$data_dir/cache_social/000123.log"
}

pack_fixture() {
  local data_dir="$1"
  local output_dir="$2"
  shift 2
  "$PACK_SCRIPT" \
    --data-dir "$data_dir" \
    --output-dir "$output_dir" \
    "$@" >/dev/null

  assert_glob_count "$output_dir" 'metaso-p2p-bootstrap-mainnet-*.tar.gz' 1
  find "$output_dir" -maxdepth 1 -type f -name 'metaso-p2p-bootstrap-mainnet-*.tar.gz' | head -n 1
}

test_pack_excludes_cache_and_writes_manifest() {
  local case_dir="$TMP_ROOT/pack-default"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"

  assert_file_exists "$unpack_dir/manifest.json"
  assert_file_exists "$unpack_dir/checksums.txt"
  assert_file_exists "$unpack_dir/namespaces/indexer_meta/000001.sst"
  assert_file_exists "$unpack_dir/namespaces/social/MANIFEST-000001"
  [[ ! -e "$unpack_dir/namespaces/cache_social" ]] || fail "cache namespace should be excluded by default"

  assert_contains "$unpack_dir/manifest.json" '"schemaVersion": 1'
  assert_contains "$unpack_dir/manifest.json" '"metasoVersion": "'
  assert_contains "$unpack_dir/manifest.json" '"gitCommit": "'
  assert_contains "$unpack_dir/manifest.json" '"builtAt": "'
  assert_contains "$unpack_dir/manifest.json" '"network": "mainnet"'
  assert_contains "$unpack_dir/manifest.json" '"sourceNode": "source-a"'
  assert_json_string_value "$unpack_dir/manifest.json" network "mainnet"
  assert_json_string_value "$unpack_dir/manifest.json" sourceNode "source-a"
  assert_contains "$unpack_dir/manifest.json" '"dataDirFormat": "pebble-per-namespace"'
  assert_contains "$unpack_dir/manifest.json" '"includedNamespaces": ['
  assert_contains "$unpack_dir/manifest.json" '"indexer_meta"'
  assert_contains "$unpack_dir/manifest.json" '"social"'
  assert_not_contains "$unpack_dir/manifest.json" '"cache_social"'

  pass "default pack excludes cache_* and writes manifest.json"
}

test_pack_manifest_escapes_control_chars() {
  local case_dir="$TMP_ROOT/manifest-control-chars"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local source_node=$'source-line-1\nsource\tline-2'
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node "$source_node")
  extract_archive "$archive" "$unpack_dir"

  assert_contains "$unpack_dir/manifest.json" '\n'
  assert_contains "$unpack_dir/manifest.json" '\t'
  assert_json_string_value "$unpack_dir/manifest.json" sourceNode "$source_node"
  pass "manifest escapes control characters in free-form metadata"
}

test_pack_include_cache_adds_cache_namespaces() {
  local case_dir="$TMP_ROOT/include-cache"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node source-a --include-cache)
  extract_archive "$archive" "$unpack_dir"

  assert_file_exists "$unpack_dir/namespaces/cache_social/000123.log"
  assert_json_array_contains "$unpack_dir/manifest.json" includedNamespaces cache_social
  pass "pack --include-cache includes cache namespaces in payload and manifest"
}

test_pack_rejects_selected_namespace_symlinks() {
  local case_dir="$TMP_ROOT/pack-symlink"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"
  ln -s /tmp/metaso-bootstrap-host-target "$data_dir/social/host-link"

  run_expect_fail pack_symlink \
    "$PACK_SCRIPT" \
    --data-dir "$data_dir" \
    --output-dir "$output_dir" \
    --network mainnet \
    --source-node source-a
  assert_contains "$TMP_ROOT/pack_symlink.stderr" "symlink"
  assert_glob_count "$output_dir" 'metaso-p2p-bootstrap-mainnet-*.tar.gz' 0
  pass "pack rejects symlinks inside selected namespaces"
}

test_restore_rejects_checksum_mismatch() {
  local case_dir="$TMP_ROOT/checksum"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  printf 'tampered\n' >>"$unpack_dir/namespaces/social/MANIFEST-000001"
  mkdir -p "$repack_dir"
  local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-tampered.tar.gz"
  tar -czf "$bad_archive" -C "$unpack_dir" .

  run_expect_fail checksum_mismatch \
    "$RESTORE_SCRIPT" \
    --archive "$bad_archive" \
    --target-dir "$target_dir"
  assert_contains "$TMP_ROOT/checksum_mismatch.stderr" "checksum"
  pass "restore verifies checksums before copying"
}

test_restore_rejects_non_empty_target_without_force() {
  local case_dir="$TMP_ROOT/non-empty"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$target_dir"
  printf 'old\n' >"$target_dir/existing.txt"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node source-a)

  run_expect_fail non_empty_target \
    "$RESTORE_SCRIPT" \
    --archive "$archive" \
    --target-dir "$target_dir"
  assert_contains "$TMP_ROOT/non_empty_target.stderr" "non-empty"
  pass "restore refuses non-empty target without --force"
}

test_restore_force_backs_up_and_replaces_target() {
  local case_dir="$TMP_ROOT/force"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$target_dir/old_namespace"
  printf 'obsolete\n' >"$target_dir/old_namespace/value.txt"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node source-a)

  "$RESTORE_SCRIPT" \
    --archive "$archive" \
    --target-dir "$target_dir" \
    --force

  assert_file_exists "$target_dir/indexer_meta/000001.sst"
  assert_file_exists "$target_dir/social/MANIFEST-000001"
  [[ ! -e "$target_dir/old_namespace" ]] || fail "target should be replaced during forced restore"

  local backup_parent
  backup_parent=$(dirname "$target_dir")
  local backup_name
  backup_name=$(find "$backup_parent" -maxdepth 1 -type d -name 'target.backup-*' -print | head -n 1)
  [[ -n "$backup_name" ]] || fail "expected forced restore backup directory"
  assert_file_exists "$backup_name/old_namespace/value.txt"
  pass "restore --force backs up and replaces target"
}

test_restore_rejects_symlinks_in_archive() {
  local case_dir="$TMP_ROOT/restore-symlink"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  ln -s /tmp/metaso-bootstrap-restore-target "$unpack_dir/namespaces/social/host-link"

  local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-symlink.tar.gz"
  tar -czf "$bad_archive" -C "$unpack_dir" .

  run_expect_fail restore_symlink \
    "$RESTORE_SCRIPT" \
    --archive "$bad_archive" \
    --target-dir "$target_dir"
  assert_contains "$TMP_ROOT/restore_symlink.stderr" "symlink"
  pass "restore rejects symlinks from unpacked archives"
}

main() {
  TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/metaso-bootstrap-test.XXXXXX")
  require_scripts_present

  test_pack_excludes_cache_and_writes_manifest
  test_pack_manifest_escapes_control_chars
  test_pack_include_cache_adds_cache_namespaces
  test_pack_rejects_selected_namespace_symlinks
  test_restore_rejects_checksum_mismatch
  test_restore_rejects_non_empty_target_without_force
  test_restore_force_backs_up_and_replaces_target
  test_restore_rejects_symlinks_in_archive

  echo "All bootstrap script tests passed."
}

main "$@"
