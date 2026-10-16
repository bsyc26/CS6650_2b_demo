#!/usr/bin/env bash
set -euo pipefail
hw5_root=$(cd "$(dirname "$0")/.." && pwd)
if [[ -d /Applications/Docker.app/Contents/Resources/bin ]]; then
  export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
fi
export RUN_ID="${RUN_ID:-http-50}"
export USER_CLASS="${USER_CLASS:-ProductHttpUser}"
export USERS="${USERS:-50}"
export SPAWN_RATE="${SPAWN_RATE:-10}"
export RUN_TIME="${RUN_TIME:-90s}"
export WORKERS="${WORKERS:-1}"
export WAIT_MIN="${WAIT_MIN:-0.1}"
export WAIT_MAX="${WAIT_MAX:-0.3}"
export WEB_PORT="${WEB_PORT:-8089}"
if [[ ! "$RUN_ID" =~ ^[a-z0-9][a-z0-9-]*$ ]]; then
  echo 'RUN_ID must contain only lowercase letters, numbers and hyphens.' >&2
  exit 1
fi
if [[ -z "${TARGET_URL:-}" ]]; then
  if [[ "${1:-up}" == up ]]; then
    hw5_endpoint=$(python3 "$hw5_root/scripts/ecs_endpoint.py")
    export TARGET_URL="${hw5_endpoint%/v1}"
  else
    # Compose validates this variable even for down/logs; neither sends traffic.
    export TARGET_URL=http://127.0.0.1:8080
  fi
fi
hw5_compose=(docker compose -p "hw5-locust-$RUN_ID" -f "$hw5_root/locust/compose.yaml")
case "${1:-up}" in
  up)
    mkdir -p "$hw5_root/evidence/locust"
    # A separate manifest preserves settings that stopped UI screenshots omit.
    python3 - "$hw5_root/evidence/locust/$RUN_ID-settings.json" <<'PY'
import datetime, json, os, sys
keys = ["RUN_ID", "USER_CLASS", "USERS", "SPAWN_RATE", "RUN_TIME", "WORKERS", "WAIT_MIN", "WAIT_MAX", "TARGET_URL", "WEB_PORT"]
settings = {key: os.environ[key] for key in keys}
settings.update(locust_version="2.46.5", get_post_weight="4:1", started_at_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
with open(sys.argv[1], "w") as output:
    json.dump(settings, output, indent=2)
    output.write("\n")
PY
    "${hw5_compose[@]}" up -d --scale "worker=$WORKERS"
    printf 'Real Locust Web UI: http://127.0.0.1:%s\n' "$WEB_PORT"
    ;;
  down) "${hw5_compose[@]}" down ;;
  logs) "${hw5_compose[@]}" logs --no-color ;;
  *) echo 'Usage: locust.sh [up|down|logs]' >&2; exit 1 ;;
esac
