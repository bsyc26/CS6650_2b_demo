#!/usr/bin/env bash
set -euo pipefail
hw5_root=$(cd "$(dirname "$0")/.." && pwd)
if [[ -d /Applications/Docker.app/Contents/Resources/bin ]]; then
  export PATH="/Applications/Docker.app/Contents/Resources/bin:$PATH"
fi
if [[ ! -f "$hw5_root/.env.observability" ]]; then
  python3 - "$hw5_root/.env.observability" <<'PY'
import os, secrets, sys
fd = os.open(sys.argv[1], os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
with os.fdopen(fd, "w") as output:
    output.write("PRODUCT_API_KEY=" + secrets.token_urlsafe(32) + "\n")
    output.write("GRAFANA_ADMIN_PASSWORD=" + secrets.token_urlsafe(32) + "\n")
print("Created ignored local credentials; their values are hidden.")
PY
fi
set -a
. "$hw5_root/.env.observability"
set +a
hw5_compose=(docker compose -p hw5-observability --env-file "$hw5_root/.env.observability" -f "$hw5_root/observability/compose.yaml")
case "${1:-up}" in
  up)
    mkdir -p "$hw5_root/evidence/observability"
    "${hw5_compose[@]}" up -d --build product prometheus grafana
    printf 'Grafana: http://127.0.0.1:13000/d/hw5-product-red/hw5-product-api-red\nPrometheus: http://127.0.0.1:19090\nProduct API: http://127.0.0.1:18088\n'
    ;;
  load)
    mkdir -p "$hw5_root/evidence/observability"
    "${hw5_compose[@]}" --profile load up -d --no-deps --force-recreate --scale locust-worker=2 locust-master locust-worker
    printf 'Locust: http://127.0.0.1:18089 (automatically stops load after the configured duration)\n'
    ;;
  stop-load) "${hw5_compose[@]}" stop locust-master locust-worker ;;
  down) "${hw5_compose[@]}" --profile load down ;;
  status) "${hw5_compose[@]}" --profile load ps ;;
  logs) "${hw5_compose[@]}" --profile load logs --no-color ;;
  *) echo 'Usage: observability.sh [up|load|stop-load|down|status|logs]' >&2; exit 1 ;;
esac
