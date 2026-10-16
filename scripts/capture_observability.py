#!/usr/bin/env python3
"""Record a real idle/load experiment and fixed Grafana evidence URLs.

Start the local stack with observability.sh up first. This collector uses only
local APIs and Docker; browser screenshots are taken separately from Grafana.
"""

import argparse
import atexit
from collections import Counter, defaultdict
import csv
import datetime as dt
import json
import math
import os
from pathlib import Path
import re
import subprocess
import tempfile
import time
from urllib.parse import urlencode
from urllib.request import urlopen


ROOT = Path(__file__).resolve().parents[1]
EVIDENCE = ROOT / "evidence/observability"
PROMETHEUS = "http://127.0.0.1:19090"


def get_json(url):
    with urlopen(url, timeout=20) as response:
        return json.load(response)


def metrics_text():
    with urlopen("http://127.0.0.1:18088/metrics", timeout=15) as response:
        return response.read().decode()


def counters(text):
    result = Counter()
    for line in text.splitlines():
        if not line.startswith("product_http_requests_total{"):
            continue
        labels = dict(re.findall(r'(\w+)="([^"]*)"', line))
        if labels["endpoint"].startswith("/v1/products/"):
            result[labels["method"] + " " + labels["endpoint"] + " " + labels["status"]] += int(float(line.rsplit(" ", 1)[1]))
    return result


def save_json(path, value):
    path.write_text(json.dumps(value, indent=2, allow_nan=False) + "\n")


def snapshot(run_id, phase, start, end):
    dashboard = json.loads((ROOT / "observability/grafana/dashboards/product-red.json").read_text())
    results = {}
    for panel in dashboard["panels"]:
        for target in panel.get("targets", []):
            expr = target["expr"]
            instant = get_json(PROMETHEUS + "/api/v1/query?" + urlencode({"query": expr, "time": end}))
            history = get_json(PROMETHEUS + "/api/v1/query_range?" + urlencode({"query": expr, "start": start, "end": end, "step": 5}))
            if instant["status"] != "success" or history["status"] != "success":
                raise RuntimeError("Prometheus rejected dashboard query: " + expr)
            results[f"panel_{panel['id']}_{target['refId']}"] = {"title": panel["title"], "expr": expr, "instant": instant["data"], "range": history["data"]}
    for name, expr in {
        "process_cpu_cores": 'rate(process_cpu_seconds_total{job="product"}[20s])',
        "process_resident_bytes": 'process_resident_memory_bytes{job="product"}',
        "go_goroutines": 'go_goroutines{job="product"}',
    }.items():
        instant = get_json(PROMETHEUS + "/api/v1/query?" + urlencode({"query": expr, "time": end}))
        history = get_json(PROMETHEUS + "/api/v1/query_range?" + urlencode({"query": expr, "start": start, "end": end, "step": 5}))
        if instant["status"] != "success" or history["status"] != "success":
            raise RuntimeError("Prometheus rejected a diagnostic query")
        results[name] = {"expr": expr, "instant": instant["data"], "range": history["data"]}
    save_json(EVIDENCE / f"{run_id}-{phase}-prometheus.json", results)
    return results


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--idle-seconds", type=int, default=90)
    parser.add_argument("--load-seconds", type=int, default=120)
    parser.add_argument("--users", type=int, default=200)
    parser.add_argument("--run-id", default="red-" + dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d-%H%M%S"))
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z0-9][a-z0-9-]*", args.run_id):
        parser.error("run-id must use lowercase letters, numbers and hyphens")
    if args.idle_seconds < 45 or args.load_seconds < 45 or args.users < 1:
        parser.error("each phase must last at least 45 seconds, with a positive user count")
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    manifest_path = EVIDENCE / f"{args.run_id}-experiment.json"
    if manifest_path.exists():
        parser.error("run-id already exists; use a fresh ID to preserve evidence")
    targets = get_json(PROMETHEUS + "/api/v1/targets")["data"]["activeTargets"]
    if len(targets) != 1 or targets[0]["health"] != "up":
        raise RuntimeError("Product scrape target is not healthy")
    get_json("http://127.0.0.1:13000/api/dashboards/uid/hw5-product-red")
    before = metrics_text()
    (EVIDENCE / f"{args.run_id}-metrics-before.txt").write_text(before)
    idle_start = time.time()
    # Stream from the beginning: at high throughput the Docker log ring can
    # rotate before an after-the-fact docker logs call reads the whole run.
    raw_path = Path(tempfile.gettempdir()) / f"hw5-{args.run_id}-product.jsonl"
    raw_output = raw_path.open("w")
    since = dt.datetime.fromtimestamp(idle_start, dt.timezone.utc).isoformat()
    docker = "/Applications/Docker.app/Contents/Resources/bin/docker" if Path("/Applications/Docker.app/Contents/Resources/bin/docker").exists() else "docker"
    log_stream = subprocess.Popen([docker, "logs", "--since", since, "--follow", "hw5-observability-product-1"], stdout=raw_output)

    def stop_log_stream():
        if log_stream.poll() is None:
            log_stream.terminate()
            try:
                log_stream.wait(timeout=5)
            except subprocess.TimeoutExpired:
                log_stream.kill()
                log_stream.wait()
        raw_output.close()

    atexit.register(stop_log_stream)
    manifest = {"run_id": args.run_id, "users": args.users, "workers": 2, "spawn_rate": 50,
                "configured_load_seconds": args.load_seconds, "think_time_seconds": 0,
                "mix_weights": {"GET_200": 80, "POST_204": 18, "GET_404_expected": 1, "POST_400_expected": 1},
                "product_cpu_limit": 0.25, "product_memory_mib": 512,
                "scrape_interval_seconds": 5, "rate_window_seconds": 20,
                "idle": {"start": idle_start}, "state": "idle"}
    save_json(manifest_path, manifest)
    print(f"{args.run_id}: idle phase started ({args.idle_seconds}s)", flush=True)
    time.sleep(args.idle_seconds)
    idle_end = time.time()
    # Ignore the first 25s so preceding contract checks cannot affect idle rates.
    idle = snapshot(args.run_id, "idle", idle_start + 25, idle_end)
    for key in ("panel_1_A", "panel_2_A"):
        values = idle[key]["instant"]["result"]
        if not values or any(float(x["value"][1]) != 0 for x in values):
            raise RuntimeError("Idle RPS/error ratio is not zero; check for other traffic")
    manifest["idle"]["end"] = idle_end
    manifest["state"] = "loading"
    save_json(manifest_path, manifest)
    print("Idle verified: zero Product RPS and zero non-2xx ratio. Starting Locust.", flush=True)
    env = dict(os.environ, RED_RUN_ID=args.run_id, RED_USERS=str(args.users), RED_RUN_TIME=f"{args.load_seconds}s", RED_SPAWN_RATE="50")
    subprocess.run([str(ROOT / "scripts/observability.sh"), "load"], env=env, check=True)
    history_path = EVIDENCE / f"{args.run_id}_stats_history.csv"
    first_active = last_active = None
    deadline = time.monotonic() + args.load_seconds + 90
    while time.monotonic() < deadline:
        if history_path.exists():
            with history_path.open() as source:
                rows = list(csv.DictReader(source))
            for row in rows:
                if row.get("Name") != "Aggregated":
                    continue
                if int(row["User Count"]) > 0:
                    timestamp = float(row["Timestamp"])
                    first_active = timestamp if first_active is None else min(first_active, timestamp)
                    last_active = timestamp if last_active is None else max(last_active, timestamp)
            if first_active is not None and rows and int(rows[-1]["User Count"]) == 0:
                break
        time.sleep(5)
    else:
        subprocess.run([str(ROOT / "scripts/observability.sh"), "stop-load"], check=True)
        raise RuntimeError("Locust did not reach a stopped state within its deadline")
    if first_active is None or last_active is None:
        raise RuntimeError("No active Locust samples were recorded")
    print("Locust load ended. Waiting for final scrapes and worker reports.", flush=True)
    time.sleep(12)
    stop_log_stream()
    after = metrics_text()
    (EVIDENCE / f"{args.run_id}-metrics-after.txt").write_text(after)
    snapshot(args.run_id, "loaded", first_active, last_active)
    with (EVIDENCE / f"{args.run_id}_stats.csv").open() as source:
        stats = list(csv.DictReader(source))
    total = next(row for row in stats if row["Name"] == "Aggregated")
    # Keep the complete transient log outside Git and save only an excerpt plus
    # aggregate evidence in the repository.
    observed = Counter()
    durations = []
    examples = defaultdict(list)
    with raw_path.open() as source:
        for line in source:
            try:
                entry = json.loads(line)
            except json.JSONDecodeError:
                continue
            if not entry.get("endpoint", "").startswith("/v1/products/"):
                continue
            key = f"{entry['method']} {entry['endpoint']} {entry['status']}"
            observed[key] += 1
            durations.append(entry["duration_ms"])
            if len(examples[key]) < 5:
                examples[key].append(entry)
    delta = counters(after) - counters(before)
    if observed != delta:
        raise RuntimeError(f"Log/counter mismatch: logs={observed}, counters={delta}; preserve raw log {raw_path}")
    excerpt_path = EVIDENCE / f"{args.run_id}-server-excerpt.jsonl"
    excerpt_path.write_text("".join(json.dumps(entry) + "\n" for entries in examples.values() for entry in entries))
    durations.sort()
    non_2xx = sum(count for key, count in observed.items() if not 200 <= int(key.rsplit(" ", 1)[1]) < 300)
    log_summary = {"requests": sum(observed.values()), "non_2xx": non_2xx,
                   "non_2xx_percent": 100 * non_2xx / sum(observed.values()),
                   "by_method_endpoint_status": dict(observed), "prometheus_counter_delta": dict(delta),
                   "log_counter_match": True, "mean_duration_ms": sum(durations) / len(durations),
                   "p50_duration_ms": durations[math.ceil(.5 * len(durations)) - 1],
                   "p95_duration_ms": durations[math.ceil(.95 * len(durations)) - 1],
                   "percentile_method": "nearest-rank from rounded JSON duration_ms; dashboard uses histogram estimates",
                   "transient_complete_log": str(raw_path)}
    save_json(EVIDENCE / f"{args.run_id}-log-summary.json", log_summary)
    manifest.update(state="complete", load={"start": first_active, "end": last_active},
                    locust_summary=total, log_summary=log_summary)
    manifest["grafana_urls"] = {}
    for phase, start, end in [("Idle", idle_start + 25, idle_end), ("Loaded", first_active, last_active)]:
        manifest["grafana_urls"][phase.lower()] = "http://127.0.0.1:13000/d/hw5-product-red/hw5-product-api-red?" + urlencode({
            "orgId": 1, "from": int(start * 1000), "to": int(end * 1000), "var-phase": phase, "kiosk": "true"})
    save_json(manifest_path, manifest)
    print(json.dumps({"run_id": args.run_id, "requests": total["Request Count"], "unexpected_locust_failures": total["Failure Count"],
                      "server_non_2xx_percent": log_summary["non_2xx_percent"], "log_counter_match": True,
                      "grafana_urls": manifest["grafana_urls"]}, indent=2), flush=True)
    subprocess.run([str(ROOT / "scripts/observability.sh"), "stop-load"], check=True)


if __name__ == "__main__":
    try:
        main()
    except Exception:
        # Cleanup load even if evidence verification fails; leave the app and
        # monitoring stack available for inspection.
        subprocess.run([str(ROOT / "scripts/observability.sh"), "stop-load"], check=False)
        raise
