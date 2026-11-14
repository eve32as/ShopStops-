#!/usr/bin/env bash
set -euo pipefail

log() {
  printf '[%s] %s\n' "$(date -u +'%Y-%m-%dT%H:%M:%SZ')" "$*"
}

die() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "Usage: $0 <region-slug> [osm-pbf-path]"
  echo "Example: $0 north-america/us/new-york /path/to/new-york.osm.pbf"
  echo "Smaller examples (faster/lower memory):"
  echo "  $0 north-america/us/new-york-latest"
  echo "  $0 north-america/us/new-york/city"
  exit 1
fi

region_slug="$1"
input_pbf_path="${2:-}"
osrm_image="${OSRM_IMAGE:-ghcr.io/project-osrm/osrm-backend}"
force_rebuild="${OSRM_FORCE_REBUILD:-0}"
osrm_threads="${OSRM_THREADS:-4}"
osrm_min_docker_mem_gb="${OSRM_MIN_DOCKER_MEM_GB:-8}"
osrm_low_memory_ok="${OSRM_LOW_MEMORY_OK:-0}"
osrm_docker_retries="${OSRM_DOCKER_RETRIES:-3}"
osrm_retry_delay_seconds="${OSRM_RETRY_DELAY_SECONDS:-5}"
osrm_reuse_cached_pbf="${OSRM_REUSE_CACHED_PBF:-1}"

if ! command -v docker >/dev/null 2>&1; then
  die "docker is required but was not found on PATH."
fi

if ! docker info >/dev/null 2>&1; then
  die "docker daemon is not reachable. Start Docker Desktop and retry."
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out_root="$repo_root/data/osrm"
region_dir="$out_root/$region_slug"
mkdir -p "$region_dir"
profile_file="${OSRM_PROFILE_FILE:-$repo_root/scripts/osrm-profiles/car_latest_supported.lua}"

region_name="${region_slug##*/}"
if [[ -z "$region_name" ]]; then
  die "invalid region slug '$region_slug'"
fi

local_pbf="$region_dir/$region_name.osm.pbf"
lock_dir="$region_dir/.prepare-osrm.lock"
current_step=""

check_docker_memory_for_extract() {
  local pbf_path="$1"
  local pbf_size_bytes docker_mem_bytes min_mem_bytes

  if [[ "$osrm_low_memory_ok" == "1" ]]; then
    log "OSRM_LOW_MEMORY_OK=1 set. Skipping Docker memory preflight."
    return 0
  fi

  if ! [[ "$osrm_min_docker_mem_gb" =~ ^[0-9]+$ ]] || [[ "$osrm_min_docker_mem_gb" -lt 1 ]]; then
    die "OSRM_MIN_DOCKER_MEM_GB must be a positive integer"
  fi

  pbf_size_bytes="$(stat -f %z "$pbf_path" 2>/dev/null || echo 0)"
  docker_mem_bytes="$(docker info --format '{{.MemTotal}}' 2>/dev/null || echo 0)"
  # Use decimal GB here to align with Docker Desktop's memory slider labels.
  min_mem_bytes=$((osrm_min_docker_mem_gb * 1000 * 1000 * 1000))

  if ! [[ "$pbf_size_bytes" =~ ^[0-9]+$ ]]; then
    pbf_size_bytes=0
  fi

  if ! [[ "$docker_mem_bytes" =~ ^[0-9]+$ ]] || [[ "$docker_mem_bytes" -le 0 ]]; then
    log "Warning: unable to detect Docker memory limit; continuing build."
    return 0
  fi

  if [[ "$pbf_size_bytes" -ge $((300 * 1024 * 1024)) ]] && [[ "$docker_mem_bytes" -lt "$min_mem_bytes" ]]; then
    die "Docker memory is too low for this dataset (${docker_mem_bytes} bytes detected, need at least ${min_mem_bytes} bytes). Increase Docker Desktop memory to >= ${osrm_min_docker_mem_gb} GB, then rerun. Override with OSRM_LOW_MEMORY_OK=1."
  fi
}

cleanup_partial_artifacts() {
  find "$region_dir" -maxdepth 1 -type f -name "$region_name.osrm*" -delete
}

has_extract_artifacts() {
  [[ -s "$region_dir/$region_name.osrm.names" ]] &&
  [[ -s "$region_dir/$region_name.osrm.properties" ]] &&
  [[ -s "$region_dir/$region_name.osrm.timestamp" ]] &&
  [[ -s "$region_dir/$region_name.osrm.datasource_names" ]] &&
  [[ -s "$region_dir/$region_name.osrm.edges" ]] &&
  [[ -s "$region_dir/$region_name.osrm.geometry" ]]
}

has_runtime_artifacts() {
  [[ -f "$region_dir/$region_name.osrm.fileIndex" ]] &&
  [[ -f "$region_dir/$region_name.osrm.ramIndex" ]] &&
  [[ -f "$region_dir/$region_name.osrm.ebg_nodes" ]] &&
  [[ -f "$region_dir/$region_name.osrm.tld" ]] &&
  [[ -f "$region_dir/$region_name.osrm.tls" ]] &&
  [[ -f "$region_dir/$region_name.osrm.icd" ]] &&
  [[ -f "$region_dir/$region_name.osrm.turn_duration_penalties" ]] &&
  [[ -f "$region_dir/$region_name.osrm.turn_weight_penalties" ]] &&
  [[ -f "$region_dir/$region_name.osrm.maneuver_overrides" ]]
}

run_docker_step_with_retries() {
  local step_name="$1"
  shift

  local attempt=1
  while true; do
    if "$@"; then
      return 0
    fi

    if [[ "$attempt" -ge "$osrm_docker_retries" ]]; then
      die "$step_name failed after ${attempt} attempt(s)"
    fi

    log "$step_name failed on attempt ${attempt}/${osrm_docker_retries}; retrying in ${osrm_retry_delay_seconds}s"
    sleep "$osrm_retry_delay_seconds"
    attempt=$((attempt + 1))
  done
}

is_dataset_complete() {
  local has_common=0
  local has_mld=0
  local has_legacy=0

  if has_extract_artifacts; then
    has_common=1
  fi

  if [[ -s "$region_dir/$region_name.osrm.cnbg" ]] &&
     [[ -s "$region_dir/$region_name.osrm.cnbg_to_ebg" ]] &&
     [[ -s "$region_dir/$region_name.osrm.nbg_nodes" ]]; then
    has_mld=1
  fi

  if [[ -s "$region_dir/$region_name.osrm.datasource_names" ]] &&
     [[ -s "$region_dir/$region_name.osrm.ebg_nodes" ]] &&
     [[ -s "$region_dir/$region_name.osrm.edges" ]] &&
     [[ -s "$region_dir/$region_name.osrm.geometry" ]]; then
    has_legacy=1
  fi

  if [[ "$has_common" -eq 1 ]] && [[ "$has_mld" -eq 1 || "$has_legacy" -eq 1 ]] && has_runtime_artifacts; then
    return 0
  fi
  return 1
}

on_error() {
  if [[ "$current_step" == "extract" ]]; then
    log "Build failed during extract. Removing partial OSRM artifacts (keeping PBF)."
    cleanup_partial_artifacts
    return
  fi

  log "Build failed during ${current_step:-unknown step}. Keeping extracted files for resume."
}

if ! mkdir "$lock_dir" 2>/dev/null; then
  die "another prepare-osrm-data run is already active for '$region_slug'"
fi
trap 'rm -rf "$lock_dir"' EXIT
trap on_error ERR

if is_dataset_complete && [[ "$force_rebuild" != "1" ]]; then
  log "Dataset already complete in $region_dir. Set OSRM_FORCE_REBUILD=1 to rebuild."
  exit 0
fi

if [[ "$force_rebuild" == "1" ]]; then
  log "OSRM_FORCE_REBUILD=1 set. Clearing existing OSRM artifacts."
  cleanup_partial_artifacts
fi

if [[ -n "$input_pbf_path" ]]; then
  if [[ ! -f "$input_pbf_path" ]]; then
    die "input PBF not found at '$input_pbf_path'"
  fi

  if [[ "$input_pbf_path" != "$local_pbf" ]]; then
    log "Copying source PBF into region directory."
    cp "$input_pbf_path" "$local_pbf"
  fi
else
  if [[ "$force_rebuild" == "1" ]]; then
    log "OSRM_FORCE_REBUILD=1 set. Re-downloading a clean PBF file."
  elif [[ "$osrm_reuse_cached_pbf" == "1" ]] && [[ -s "$local_pbf" ]]; then
    log "Using cached PBF at $local_pbf. Set OSRM_REUSE_CACHED_PBF=0 to force a fresh download."
  else
    geofabrik_url="https://download.geofabrik.de/$region_slug-latest.osm.pbf"
    local_pbf_tmp="$region_dir/$region_name.osm.pbf.download"
    rm -f "$local_pbf_tmp"
    if [[ -s "$local_pbf" ]]; then
      log "Replacing existing PBF with a clean full download to avoid truncated/partial file errors."
      rm -f "$local_pbf"
    else
      log "Downloading: $geofabrik_url"
    fi
    curl --fail --location --retry 5 --retry-delay 2 --retry-connrefused \
      "$geofabrik_url" --output "$local_pbf_tmp"
    mv -f "$local_pbf_tmp" "$local_pbf"
  fi
fi

if [[ ! -s "$local_pbf" ]]; then
  die "local PBF is missing or empty: $local_pbf"
fi

check_docker_memory_for_extract "$local_pbf"

if [[ ! -f "$profile_file" ]]; then
  die "OSRM profile file not found: $profile_file"
fi

if ! [[ "$osrm_threads" =~ ^[0-9]+$ ]] || [[ "$osrm_threads" -lt 1 ]]; then
  die "OSRM_THREADS must be a positive integer"
fi

if ! [[ "$osrm_docker_retries" =~ ^[0-9]+$ ]] || [[ "$osrm_docker_retries" -lt 1 ]]; then
  die "OSRM_DOCKER_RETRIES must be a positive integer"
fi

log "Preparing OSRM dataset in $region_dir"
if has_extract_artifacts && [[ "$force_rebuild" != "1" ]]; then
  log "Existing extract artifacts found; skipping extract and resuming from partition/customize."
else
  current_step="extract"
  run_docker_step_with_retries "osrm-extract" \
    docker run --rm \
      -v "$region_dir:/data" \
      -v "$profile_file:/opt/car.lua:ro" \
      "$osrm_image" \
      osrm-extract --threads "$osrm_threads" -p /opt/car.lua "/data/$region_name.osm.pbf"
fi

current_step="partition"
run_docker_step_with_retries "osrm-partition" \
  docker run --rm -v "$region_dir:/data" "$osrm_image" \
    osrm-partition "/data/$region_name.osrm"

current_step="customize"
run_docker_step_with_retries "osrm-customize" \
  docker run --rm -v "$region_dir:/data" "$osrm_image" \
    osrm-customize "/data/$region_name.osrm"

if ! is_dataset_complete; then
  die "dataset build finished but required OSRM files are still missing"
fi

log "Done. OSRM files are under: $region_dir"
