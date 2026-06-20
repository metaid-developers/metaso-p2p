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

extract_prefixed_value() {
  local file="$1"
  local prefix="$2"
  awk -v prefix="$prefix: " '
    index($0, prefix) == 1 {
      print substr($0, length(prefix) + 1)
      found = 1
      exit
    }
    END {
      if (!found) {
        exit 1
      }
    }
  ' "$file"
}

assert_manifest_summary_value() {
  local file="$1"
  local key="$2"
  local raw_expected="$3"
  local summary_json=""

  summary_json=$(extract_prefixed_value "$file" manifest) || \
    fail "expected manifest summary line in $file"
  "$PYTHON_BIN" - "$summary_json" "$key" "$raw_expected" <<'PY'
import json
import sys

summary_json, key, raw_expected = sys.argv[1:4]
summary = json.loads(summary_json)
expected = json.loads(raw_expected)
actual = summary.get(key)
if actual != expected:
    raise SystemExit(f"expected manifest summary {key}={expected!r}, got {actual!r}")
PY
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

repack_archive() {
  local source_dir="$1"
  local archive="$2"
  tar -czf "$archive" -C "$source_dir" .
}

update_manifest_checksum() {
  local manifest_file="$1"
  local checksums_file="$2"
  "$PYTHON_BIN" - "$manifest_file" "$checksums_file" <<'PY'
import hashlib
import sys

manifest_path, checksums_path = sys.argv[1:3]
digest = hashlib.sha256()
with open(manifest_path, "rb") as handle:
    digest.update(handle.read())

updated = False
output_lines = []
with open(checksums_path, "r", encoding="utf-8") as handle:
    for raw_line in handle:
        line = raw_line.rstrip("\n")
        if not line:
            continue
        if line.endswith("  manifest.json"):
            output_lines.append(f"{digest.hexdigest()}  manifest.json")
            updated = True
        else:
            output_lines.append(line)

if not updated:
    raise SystemExit("manifest.json missing from checksums.txt")

with open(checksums_path, "w", encoding="utf-8") as handle:
    for line in output_lines:
        handle.write(f"{line}\n")
PY
}

set_manifest_field() {
  local manifest_file="$1"
  local field="$2"
  local raw_json_value="$3"
  "$PYTHON_BIN" - "$manifest_file" "$field" "$raw_json_value" <<'PY'
import json
import sys

path, field, raw_value = sys.argv[1:4]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

data[field] = json.loads(raw_value)

with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2)
    handle.write("\n")
PY
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

create_fake_date_bin() {
  local bin_dir="$1"
  local fixed_timestamp="$2"
  mkdir -p "$bin_dir"
  cat >"$bin_dir/date" <<EOF
#!/usr/bin/env bash
if [[ "\$#" -eq 2 && "\$1" == "-u" && "\$2" == "+%Y%m%dT%H%M%SZ" ]]; then
  printf '%s\n' '$fixed_timestamp'
  exit 0
fi
exec /bin/date "\$@"
EOF
  chmod +x "$bin_dir/date"
}

pack_fixture() {
  local data_dir="$1"
  local output_dir="$2"
  local stdout_file="$3"
  shift 3
  "$PACK_SCRIPT" \
    --data-dir "$data_dir" \
    --output-dir "$output_dir" \
    "$@" >"$stdout_file"

  assert_glob_count "$output_dir" 'metaso-p2p-bootstrap-mainnet-*.tar.gz' 1
  local archive_path=""
  archive_path=$(extract_prefixed_value "$stdout_file" archive) || \
    fail "expected archive line in $stdout_file"
  [[ -f "$archive_path" ]] || fail "expected archive file from output: $archive_path"
  printf '%s\n' "$archive_path"
}

test_pack_excludes_cache_and_writes_manifest() {
  local case_dir="$TMP_ROOT/pack-default"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local stdout_file="$case_dir/pack.stdout"
  local unpack_dir="$case_dir/unpack"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$stdout_file" --network mainnet --source-node source-a)
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
  assert_contains "$stdout_file" 'manifest: {"network":"mainnet"'
  assert_contains "$stdout_file" '"builtAt":"'
  assert_contains "$stdout_file" '"metasoVersion":"'
  assert_contains "$stdout_file" '"gitCommit":"'
  assert_manifest_summary_value "$stdout_file" network '"mainnet"'
  assert_manifest_summary_value "$stdout_file" sourceNode '"source-a"'
  assert_manifest_summary_value "$stdout_file" includedNamespaces '["indexer_meta", "social"]'

  pass "default pack excludes cache_* and writes manifest.json"
}

test_pack_manifest_escapes_control_chars() {
  local case_dir="$TMP_ROOT/manifest-control-chars"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local stdout_file="$case_dir/pack.stdout"
  local unpack_dir="$case_dir/unpack"
  local source_node=$'source-line-1\nsource\tline-2'
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$stdout_file" --network mainnet --source-node "$source_node")
  extract_archive "$archive" "$unpack_dir"

  assert_contains "$unpack_dir/manifest.json" '\n'
  assert_contains "$unpack_dir/manifest.json" '\t'
  assert_json_string_value "$unpack_dir/manifest.json" sourceNode "$source_node"
  assert_contains "$stdout_file" '\n'
  assert_contains "$stdout_file" '\t'
  assert_manifest_summary_value "$stdout_file" sourceNode '"source-line-1\nsource\tline-2"'
  pass "manifest escapes control characters in free-form metadata"
}

test_pack_include_cache_adds_cache_namespaces() {
  local case_dir="$TMP_ROOT/include-cache"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local stdout_file="$case_dir/pack.stdout"
  local unpack_dir="$case_dir/unpack"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$stdout_file" --network mainnet --source-node source-a --include-cache)
  extract_archive "$archive" "$unpack_dir"

  assert_file_exists "$unpack_dir/namespaces/cache_social/000123.log"
  assert_json_array_contains "$unpack_dir/manifest.json" includedNamespaces cache_social
  assert_manifest_summary_value "$stdout_file" includedNamespaces '["cache_social", "indexer_meta", "social"]'
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

test_pack_rejects_filename_unsafe_network_label() {
  local case_dir="$TMP_ROOT/pack-unsafe-network"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir"

  run_expect_fail pack_unsafe_network \
    "$PACK_SCRIPT" \
    --data-dir "$data_dir" \
    --output-dir "$output_dir" \
    --network 'main net' \
    --source-node source-a
  assert_contains "$TMP_ROOT/pack_unsafe_network.stderr" "network label must match [A-Za-z0-9._-]+"
  assert_glob_count "$output_dir" 'metaso-p2p-bootstrap-*' 0
  pass "pack rejects filename-unsafe network labels"
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
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
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

test_restore_rejects_tampered_manifest_without_checksum_entry() {
  local case_dir="$TMP_ROOT/manifest-missing-checksum"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir" "$target_dir/existing_namespace"
  printf 'old\n' >"$target_dir/existing_namespace/value.txt"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  "$PYTHON_BIN" - "$unpack_dir/manifest.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

data["sourceNode"] = "tampered-source"

with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2)
    handle.write("\n")
PY
  grep -Fv '  manifest.json' "$unpack_dir/checksums.txt" >"$repack_dir/checksums.txt"
  mv "$repack_dir/checksums.txt" "$unpack_dir/checksums.txt"

  local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-tampered-manifest.tar.gz"
  tar -czf "$bad_archive" -C "$unpack_dir" .

  run_expect_fail tampered_manifest_missing_checksum \
    "$RESTORE_SCRIPT" \
    --archive "$bad_archive" \
    --target-dir "$target_dir" \
    --force
  assert_contains "$TMP_ROOT/tampered_manifest_missing_checksum.stderr" "manifest.json"
  assert_file_exists "$target_dir/existing_namespace/value.txt"
  assert_glob_count "$case_dir" 'target.backup-*' 0
  [[ ! -e "$target_dir/indexer_meta" ]] || fail "restore should fail before touching target"
  pass "restore rejects tampered manifest without checksum entry before target changes"
}

test_restore_rejects_missing_required_manifest_fields() {
  local case_dir="$TMP_ROOT/missing-required-fields"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_template="$case_dir/unpack-template"
  local repack_dir="$case_dir/repack"
  local field=""
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_template"

  for field in schemaVersion metasoVersion gitCommit builtAt network sourceNode dataDirFormat includedNamespaces; do
    local variant_dir="$case_dir/$field"
    local target_dir="$variant_dir/target"
    mkdir -p "$variant_dir"
    cp -R "$unpack_template/." "$variant_dir/"
    "$PYTHON_BIN" - "$variant_dir/manifest.json" "$field" <<'PY'
import json
import sys

path, field = sys.argv[1:3]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

del data[field]

with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, indent=2)
    handle.write("\n")
PY
    update_manifest_checksum "$variant_dir/manifest.json" "$variant_dir/checksums.txt"

    local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-missing-${field}.tar.gz"
    repack_archive "$variant_dir" "$bad_archive"

    run_expect_fail "missing_field_${field}" \
      "$RESTORE_SCRIPT" \
      --archive "$bad_archive" \
      --target-dir "$target_dir"
    assert_contains "$TMP_ROOT/missing_field_${field}.stderr" "missing required manifest field: $field"
    [[ ! -e "$target_dir" ]] || fail "restore should fail before creating target for missing field $field"
  done

  pass "restore rejects manifests missing required contract fields"
}

test_restore_rejects_semantically_invalid_manifest_values() {
  local case_dir="$TMP_ROOT/invalid-manifest-values"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_template="$case_dir/unpack-template"
  local repack_dir="$case_dir/repack"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_template"

  local case_name=""
  local field=""
  local raw_value=""
  local expected_error=""
  while IFS='|' read -r case_name field raw_value expected_error; do
    local variant_dir="$case_dir/$case_name"
    local target_dir="$variant_dir/target"
    mkdir -p "$variant_dir"
    cp -R "$unpack_template/." "$variant_dir/"
    set_manifest_field "$variant_dir/manifest.json" "$field" "$raw_value"
    update_manifest_checksum "$variant_dir/manifest.json" "$variant_dir/checksums.txt"

    local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-invalid-${case_name}.tar.gz"
    repack_archive "$variant_dir" "$bad_archive"

    run_expect_fail "invalid_manifest_${case_name}" \
      "$RESTORE_SCRIPT" \
      --archive "$bad_archive" \
      --target-dir "$target_dir"
    assert_contains "$TMP_ROOT/invalid_manifest_${case_name}.stderr" "$expected_error"
    [[ ! -e "$target_dir" ]] || fail "restore should fail before creating target for invalid manifest case $case_name"
  done <<'EOF'
invalid-builtat|builtAt|"2026-06-20 12:34:56Z"|manifest field builtAt must be a UTC RFC3339 timestamp
invalid-gitcommit|gitCommit|"deadbeef"|manifest field gitCommit must be empty or a 40-character hex SHA
unsupported-schemaversion|schemaVersion|2|unsupported manifest schemaVersion: 2
unsupported-datadirformat|dataDirFormat|"flat-directory"|unsupported manifest dataDirFormat: flat-directory
empty-sourcenode|sourceNode|""|manifest field sourceNode must be a non-empty string
malformed-namespace-entry|includedNamespaces|["social", "bad/entry"]|invalid namespace entry: bad/entry
EOF

  pass "restore rejects semantically invalid manifest field values"
}

test_restore_accepts_compact_valid_manifest_json() {
  local case_dir="$TMP_ROOT/compact-manifest"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  "$PYTHON_BIN" - "$unpack_dir/manifest.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, "r", encoding="utf-8") as handle:
    data = json.load(handle)

with open(path, "w", encoding="utf-8") as handle:
    json.dump(data, handle, separators=(",", ":"))
PY
  update_manifest_checksum "$unpack_dir/manifest.json" "$unpack_dir/checksums.txt"

  local compact_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-compact-manifest.tar.gz"
  repack_archive "$unpack_dir" "$compact_archive"

  "$RESTORE_SCRIPT" \
    --archive "$compact_archive" \
    --target-dir "$target_dir"

  assert_file_exists "$target_dir/indexer_meta/000001.sst"
  assert_file_exists "$target_dir/social/MANIFEST-000001"
  pass "restore accepts compact valid manifest.json"
}

test_restore_rejects_unlisted_payload_files() {
  local case_dir="$TMP_ROOT/unlisted-payload"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  printf 'intruder\n' >"$unpack_dir/namespaces/social/EXTRA-UNLISTED.txt"

  local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-unlisted.tar.gz"
  tar -czf "$bad_archive" -C "$unpack_dir" .

  run_expect_fail unlisted_payload \
    "$RESTORE_SCRIPT" \
    --archive "$bad_archive" \
    --target-dir "$target_dir"
  assert_contains "$TMP_ROOT/unlisted_payload.stderr" "not listed in checksums.txt"
  [[ ! -e "$target_dir" ]] || fail "target directory should not be created on payload completeness failure"
  pass "restore rejects unlisted payload files before copying"
}

test_restore_rejects_extra_archive_root_entries() {
  local case_dir="$TMP_ROOT/extra-root-entry"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  printf 'rogue\n' >"$unpack_dir/EXTRA.txt"

  local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-extra-root-entry.tar.gz"
  repack_archive "$unpack_dir" "$bad_archive"

  run_expect_fail extra_root_entry \
    "$RESTORE_SCRIPT" \
    --archive "$bad_archive" \
    --target-dir "$target_dir"
  assert_contains "$TMP_ROOT/extra_root_entry.stderr" "unexpected archive root entry"
  [[ ! -e "$target_dir" ]] || fail "target directory should not be created on archive root validation failure"
  pass "restore rejects unexpected archive root entries before copying"
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
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)

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
  local restore_stdout="$case_dir/restore.stdout"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$target_dir/old_namespace"
  printf 'obsolete\n' >"$target_dir/old_namespace/value.txt"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)

  "$RESTORE_SCRIPT" \
    --archive "$archive" \
    --target-dir "$target_dir" \
    --force >"$restore_stdout"

  assert_file_exists "$target_dir/indexer_meta/000001.sst"
  assert_file_exists "$target_dir/social/MANIFEST-000001"
  [[ ! -e "$target_dir/old_namespace" ]] || fail "target should be replaced during forced restore"
  assert_contains "$restore_stdout" 'manifest: {"network":"mainnet"'
  assert_contains "$restore_stdout" '"builtAt":"'
  assert_contains "$restore_stdout" '"metasoVersion":"'
  assert_contains "$restore_stdout" '"gitCommit":"'
  assert_manifest_summary_value "$restore_stdout" network '"mainnet"'
  assert_manifest_summary_value "$restore_stdout" sourceNode '"source-a"'
  assert_manifest_summary_value "$restore_stdout" includedNamespaces '["indexer_meta", "social"]'

  local backup_parent
  backup_parent=$(dirname "$target_dir")
  local backup_name
  backup_name=$(find "$backup_parent" -maxdepth 1 -type d -name 'target.backup-*' -print | head -n 1)
  [[ -n "$backup_name" ]] || fail "expected forced restore backup directory"
  assert_file_exists "$backup_name/old_namespace/value.txt"
  pass "restore --force backs up and replaces target"
}

test_restore_force_rejects_backup_path_collision() {
  local case_dir="$TMP_ROOT/force-backup-collision"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local target_dir="$case_dir/target"
  local fake_bin_dir="$case_dir/fake-bin"
  local fixed_timestamp="20260620T123456Z"
  local colliding_backup_dir="$case_dir/target.backup-$fixed_timestamp"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$target_dir/old_namespace" "$colliding_backup_dir/existing_collision"
  printf 'obsolete\n' >"$target_dir/old_namespace/value.txt"
  printf 'keep\n' >"$colliding_backup_dir/existing_collision/value.txt"
  create_fake_date_bin "$fake_bin_dir" "$fixed_timestamp"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)

  run_expect_fail force_backup_collision \
    env PATH="$fake_bin_dir:$PATH" \
    "$RESTORE_SCRIPT" \
    --archive "$archive" \
    --target-dir "$target_dir" \
    --force
  assert_contains "$TMP_ROOT/force_backup_collision.stderr" "backup path already exists"
  assert_file_exists "$target_dir/old_namespace/value.txt"
  assert_file_exists "$colliding_backup_dir/existing_collision/value.txt"
  [[ ! -e "$colliding_backup_dir/target" ]] || fail "forced restore must not nest target inside an existing backup sibling"
  [[ ! -e "$target_dir/indexer_meta" ]] || fail "restore should fail before copying replacement data on backup collision"
  pass "restore --force rejects backup sibling collisions"
}

test_restore_rejects_target_dir_symlink() {
  local case_dir="$TMP_ROOT/target-symlink"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local real_target_dir="$case_dir/real-target"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$real_target_dir/existing_namespace"
  printf 'old\n' >"$real_target_dir/existing_namespace/value.txt"
  ln -s "$real_target_dir" "$target_dir"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)

  run_expect_fail target_symlink \
    "$RESTORE_SCRIPT" \
    --archive "$archive" \
    --target-dir "$target_dir" \
    --force
  assert_contains "$TMP_ROOT/target_symlink.stderr" "symlink"
  [[ -L "$target_dir" ]] || fail "target symlink should remain in place"
  assert_file_exists "$real_target_dir/existing_namespace/value.txt"
  assert_glob_count "$case_dir" 'target.backup-*' 0
  [[ ! -e "$real_target_dir/indexer_meta" ]] || fail "restore should not follow target symlink"
  pass "restore rejects symlink target directories before replacement"
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
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
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

test_restore_rejects_extra_empty_namespace_directory() {
  local case_dir="$TMP_ROOT/extra-empty-namespace"
  local data_dir="$case_dir/data"
  local output_dir="$case_dir/out"
  local unpack_dir="$case_dir/unpack"
  local repack_dir="$case_dir/repack"
  local target_dir="$case_dir/target"
  create_fixture_data "$data_dir"
  mkdir -p "$output_dir" "$repack_dir" "$target_dir/existing_namespace"
  printf 'old\n' >"$target_dir/existing_namespace/value.txt"

  local archive
  archive=$(pack_fixture "$data_dir" "$output_dir" "$case_dir/pack.stdout" --network mainnet --source-node source-a)
  extract_archive "$archive" "$unpack_dir"
  mkdir -p "$unpack_dir/namespaces/rogue_empty_namespace"

  local bad_archive="$repack_dir/metaso-p2p-bootstrap-mainnet-extra-empty-namespace.tar.gz"
  tar -czf "$bad_archive" -C "$unpack_dir" .

  run_expect_fail extra_empty_namespace \
    "$RESTORE_SCRIPT" \
    --archive "$bad_archive" \
    --target-dir "$target_dir" \
    --force
  assert_contains "$TMP_ROOT/extra_empty_namespace.stderr" "unexpected directory"
  assert_file_exists "$target_dir/existing_namespace/value.txt"
  assert_glob_count "$case_dir" 'target.backup-*' 0
  [[ ! -e "$target_dir/indexer_meta" ]] || fail "restore should fail before copying into target"
  pass "restore rejects extra empty namespace directories before backup"
}

main() {
  TMP_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/metaso-bootstrap-test.XXXXXX")
  require_scripts_present

  test_pack_excludes_cache_and_writes_manifest
  test_pack_manifest_escapes_control_chars
  test_pack_include_cache_adds_cache_namespaces
  test_pack_rejects_selected_namespace_symlinks
  test_pack_rejects_filename_unsafe_network_label
  test_restore_rejects_checksum_mismatch
  test_restore_rejects_tampered_manifest_without_checksum_entry
  test_restore_rejects_missing_required_manifest_fields
  test_restore_rejects_semantically_invalid_manifest_values
  test_restore_accepts_compact_valid_manifest_json
  test_restore_rejects_unlisted_payload_files
  test_restore_rejects_extra_archive_root_entries
  test_restore_rejects_non_empty_target_without_force
  test_restore_force_backs_up_and_replaces_target
  test_restore_force_rejects_backup_path_collision
  test_restore_rejects_target_dir_symlink
  test_restore_rejects_symlinks_in_archive
  test_restore_rejects_extra_empty_namespace_directory

  echo "All bootstrap script tests passed."
}

main "$@"
