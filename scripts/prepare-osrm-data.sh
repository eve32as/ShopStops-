#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "Usage: $0 <geofabrik-region-path>" >&2
  echo "Example: $0 north-america/us/new-york" >&2
  exit 2
fi

region=$1
if ! printf '%s\n' "$region" | grep -Eq '^[a-z0-9-]+(/[a-z0-9-]+)*$'; then
  echo "Region must be a Geofabrik path containing lowercase letters, digits, and hyphens." >&2
  exit 2
fi

for command in curl docker grep; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "Required command not found: $command" >&2
    exit 2
  fi
done

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(dirname "$script_dir")
volume=${SHOPSTOPS_OSRM_VOLUME:-shopstops-osrm-data}
if ! printf '%s\n' "$volume" | grep -Eq '^[A-Za-z0-9][A-Za-z0-9_.-]*$'; then
  echo "SHOPSTOPS_OSRM_VOLUME must be a valid Docker volume name." >&2
  exit 2
fi
export SHOPSTOPS_OSRM_VOLUME="$volume"

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/shopstops-osrm.XXXXXX")
cleanup() {
  rm -rf "$work_dir"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
umask 077

map_file="$work_dir/region-latest.osm.pbf"
map_url="https://download.geofabrik.de/${region}-latest.osm.pbf"
echo "Downloading $map_url"
curl --fail --location --proto '=https' --tlsv1.2 \
  --connect-timeout 30 --retry 3 --retry-delay 2 \
  --output "$map_file" "$map_url"

compose() {
  docker compose --project-directory "$repo_root" -f "$repo_root/compose.yaml" "$@"
}

compose --profile routing pull osrm-router
compose --profile routing run --rm --no-deps \
  --volume "$work_dir:/input" \
  --entrypoint osrm-extract osrm-router \
  -p /opt/car.lua /input/region-latest.osm.pbf
compose --profile routing run --rm --no-deps \
  --volume "$work_dir:/input" \
  --entrypoint osrm-contract osrm-router \
  /input/region-latest.osrm

if [ ! -f "$work_dir/region-latest.osrm" ]; then
  echo "OSRM preprocessing did not produce region-latest.osrm." >&2
  exit 1
fi

# shellcheck disable=SC2016
compose --profile routing run --rm --no-deps \
  --volume "$work_dir:/source:ro" \
  --volume "$volume:/target" \
  --entrypoint sh osrm-router -ec '
    set -- /target/region-latest.osrm*
    if [ -e "$1" ]; then
      echo "The OSRM volume already contains region-latest.osrm; refusing to overwrite it." >&2
      exit 1
    fi
    set -- /source/region-latest.osrm*
    if [ ! -e "$1" ]; then
      echo "No preprocessed OSRM files were found." >&2
      exit 1
    fi
    cp -a "$@" /target/
  '

printf 'Installed the preprocessed region in Docker volume %s.\n' "$volume"
printf 'Start the router with: SHOPSTOPS_OSRM_VOLUME=%s docker compose --profile routing up -d osrm-router\n' "$volume"
