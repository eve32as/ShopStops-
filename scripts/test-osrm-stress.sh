#!/bin/sh
set -eu

for command in docker go grep mktemp; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command not found: $command" >&2
    exit 2
  fi
done

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(dirname "$script_dir")
env_file=${SHOPSTOPS_COMPOSE_ENV_FILE:-}
if [ -z "$env_file" ]; then
  if [ -f "$repo_root/.env" ]; then
    env_file="$repo_root/.env"
  else
    env_file="$repo_root/.env.example"
  fi
fi
if [ ! -r "$env_file" ]; then
  echo "Compose environment file is not readable: $env_file" >&2
  exit 2
fi

compose() {
  docker compose --env-file "$env_file" --project-directory "$repo_root" \
    -f "$repo_root/compose.yaml" \
    -f "$repo_root/compose.routing.yaml" \
    --profile routing "$@"
}

if ! compose ps --status running --services | grep -qx osrm-router; then
  echo "The OSRM router is not running. Start it with the documented routing Compose profile first." >&2
  exit 2
fi

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/shopstops-osrm-stress.XXXXXX")
test_binary=/tmp/shopstops-osrm-stress.test
cleanup() {
  compose exec -T osrm-router rm -f "$test_binary" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

(cd "$repo_root" && CGO_ENABLED=0 go test -c -o "$work_dir/osrm-stress.test" ./internal/routing)
chmod 755 "$work_dir/osrm-stress.test"
compose cp "$work_dir/osrm-stress.test" "osrm-router:$test_binary"
compose exec -T \
  -e SHOPSTOPS_OSRM_STRESS_URL=http://127.0.0.1:5000 \
  osrm-router "$test_binary" \
  -test.run '^TestOSRMClientStress1000Concurrent$' -test.v -test.timeout=30s
