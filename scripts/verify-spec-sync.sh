#!/bin/sh
set -eu

specs_dir=${ODP_SPECS_DIR:-../odp-specs}
schemas_dir="$specs_dir/ietf/schemas"
vectors_dir="$specs_dir/ietf/test-vectors"

for bundled in schemas/*.schema.json; do
  cmp "$bundled" "$schemas_dir/$(basename "$bundled")"
done

schema_count=$(find schemas -name '*.schema.json' -type f | wc -l | tr -d ' ')
source_count=$(find "$schemas_dir" -name '*.schema.json' -type f | wc -l | tr -d ' ')
test "$schema_count" = "$source_count"

cmp testdata/vectors/errors-limits-contract.json "$vectors_dir/errors-limits/contract.json"
cmp testdata/vectors/identity-comparison.json "$vectors_dir/identity/identity-comparison.json"
cmp testdata/vectors/identity-local-identifier.json "$vectors_dir/identity/local-identifier.json"
cmp testdata/vectors/pagination-contract.json "$vectors_dir/pagination/contract.json"
cmp testdata/vectors/service-document-validation.json "$vectors_dir/service-document/validation.json"
