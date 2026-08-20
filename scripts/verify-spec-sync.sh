#!/bin/sh
set -eu

specs_dir=${ODP_SPECS_DIR:-../odp-specs}
schemas_dir="$specs_dir/ietf/schemas"
vectors_dir="$specs_dir/ietf/test-vectors"
mode=${1:-"--check"}

if [ ! -d "$schemas_dir" ] || [ ! -d "$vectors_dir" ]; then
  echo "ODP specifications not found at $specs_dir. Set ODP_SPECS_DIR to an odp-specs checkout." >&2
  exit 1
fi

if [ "$mode" != "--check" ] && [ "$mode" != "--update" ]; then
  echo "usage: $0 [--check|--update]" >&2
  exit 2
fi

sync_file() {
  target=$1
  source=$2
  if [ "$mode" = "--update" ]; then
    cp "$source" "$target"
  else
    cmp "$target" "$source"
  fi
}

if [ "$mode" = "--update" ]; then
  for source in "$schemas_dir"/*.schema.json; do
    cp "$source" "schemas/$(basename "$source")"
  done
  for bundled in schemas/*.schema.json; do
    if [ ! -f "$schemas_dir/$(basename "$bundled")" ]; then
      rm "$bundled"
    fi
  done
else
  for bundled in schemas/*.schema.json; do
    cmp "$bundled" "$schemas_dir/$(basename "$bundled")"
  done

  schema_count=$(find schemas -name '*.schema.json' -type f | wc -l | tr -d ' ')
  source_count=$(find "$schemas_dir" -name '*.schema.json' -type f | wc -l | tr -d ' ')
  test "$schema_count" = "$source_count"
fi

sync_file testdata/vectors/errors-limits-contract.json "$vectors_dir/errors-limits/contract.json"
sync_file testdata/vectors/identity-comparison.json "$vectors_dir/identity/identity-comparison.json"
sync_file testdata/vectors/identity-local-identifier.json "$vectors_dir/identity/local-identifier.json"
sync_file testdata/vectors/pagination-contract.json "$vectors_dir/pagination/contract.json"
sync_file testdata/vectors/service-document-validation.json "$vectors_dir/service-document/validation.json"
