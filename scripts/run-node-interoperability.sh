#!/bin/sh
set -eu

node_dir=${ODP_NODE_DIR:-../odp-node}
port=${ODP_INTEROP_PORT:-4101}
service_url="http://127.0.0.1:$port"
log_file=${TMPDIR:-/tmp}/odp-node-interop.log

pnpm --dir "$node_dir" build
HOST=127.0.0.1 PORT="$port" node "$node_dir/examples/odp-service-small/dist/index.js" >"$log_file" 2>&1 &
service_pid=$!
trap 'kill "$service_pid" 2>/dev/null || true' EXIT INT TERM

attempt=0
until curl --fail --silent --output /dev/null "$service_url/.well-known/odp"; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 50 ]; then
    sed -n '1,120p' "$log_file" >&2
    exit 1
  fi
  sleep 0.1
done

go run ./cmd/odp-node-interop "$service_url"
