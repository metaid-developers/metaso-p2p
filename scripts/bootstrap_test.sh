#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
PACK_SCRIPT="$ROOT_DIR/scripts/bootstrap-pack.sh"
RESTORE_SCRIPT="$ROOT_DIR/scripts/bootstrap-restore.sh"
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
  "$PACK_SCRIPT" \
    --data-dir "$data_dir" \
    --output-dir "$output_dir" \
    --network mainnet \
    --source-node source-a >/dev/null

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
  archive=$(pack_fixture "$data_dir" "$output_dir")
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
  assert_contains "$unpack_dir/manifest.json" '"dataDirFormat": "pebble-per-namespace"'
  assert_contains "$unpack_dir/manifest.json" '"includedNamespaces": ['
  assert_contains "$unpack_dir/manifest.json" '"indexer_meta"'
  assert_contains "$unpack_dir/manifest.json" '"social"'
  assert_not_contains "$unpack_dir/manifest.json" '"cache_social"'

  pass "default pack excludes cache_* and writes manifest.json"
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
  archive=$(pack_fixture "$data_dir" "$output_dir")
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
  archive=$(pack_fixture "$data_dir" "$output_dir")

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
  archive=$(pack_fixture "$data_dir" "$output_dir")

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

main() {
  TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/metaso-bootstrap-test.XXXXXX")
  require_scripts_present

  test_pack_excludes_cache_and_writes_manifest
  test_restore_rejects_checksum_mismatch
  test_restore_rejects_non_empty_target_without_force
  test_restore_force_backs_up_and_replaces_target

  echo "All bootstrap script tests passed."
}

main "$@"
