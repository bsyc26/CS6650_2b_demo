# Local Product API observability

The local stack runs the instrumented Product API, Prometheus and Grafana through Docker Compose. Prometheus scrapes real `/metrics` data every five seconds; Grafana's provisioned dashboard queries that Prometheus service. Optional Locust master/workers generate traffic on the same Docker network. This stack is local and does not deploy monitoring services to AWS.

## Start and inspect

Prerequisites: Docker with Compose, Python 3.9 or newer, and curl. From the repository root:

```bash
scripts/observability.sh up
```

| Service | Local address | Purpose |
| --- | --- | --- |
| Product API | `http://127.0.0.1:18088` | Same Go/Gin code, including `/health` and `/metrics` |
| Prometheus | `http://127.0.0.1:19090` | Scrape targets and PromQL queries |
| Grafana | [Product API RED dashboard](http://127.0.0.1:13000/d/hw5-product-red/hw5-product-api-red) | Provisioned datasource and dashboard |
| Locust, while load services run | `http://127.0.0.1:18089` | Real load-test Statistics/Charts |

The Product container has a 0.25 CPU quota and 512 MiB memory limit. On this Mac the local image is native Linux/ARM64, while the earlier AWS image was Linux/AMD64. Docker-network latency and shared laptop resources also differ from AWS, so local RPS is not a direct comparison to the public-cloud measurements.

The wrapper generates ignored `.env.observability` with a local application key and an admin password, using private file permissions. It loads those credentials consistently for the app and Locust; their values are not printed. `/metrics` and `/health` are operational endpoints without application authentication in this local stack; Product requests still require the configured credential. Grafana permits anonymous **Viewer** access on its loopback-bound port, with the login form and sign-up disabled. Prometheus and Grafana retain data in Docker named volumes outside Git. Prometheus retention is 24 hours and at most 512 MB.

```bash
curl --fail http://127.0.0.1:18088/health
curl --fail --silent http://127.0.0.1:18088/metrics
scripts/observability.sh status
```

The datasource and dashboard are provisioned from [grafana/provisioning](grafana/provisioning) and [product-red.json](grafana/dashboards/product-red.json). Their configuration is reproducible from source and read-only in the UI. Pinned versions are Prometheus 3.15.0, Grafana 13.2.4, Prometheus Go client 1.25.0, and Locust 2.46.5.

## Metrics and logs

[metrics.go](../src/metrics.go) owns a separate registry per router. The existing request middleware records the final response status after handlers and panic recovery finish, then emits one JSON log line with the same duration. Metrics include every application route, authentication failures, validation errors, unknown products and recovered 500s. The `/metrics` route is excluded from RED counters/histograms so scrapes cannot inflate application traffic; its requests still appear in the request log. The dashboard selects the two canonical `/v1/products/...` route templates and excludes operational `/health` traffic and unversioned aliases.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `product_http_requests_total` | Counter | `method`, `endpoint`, `status` | Completed requests, including non-2xx |
| `product_http_errors_total` | Counter | `method`, `endpoint` | Completed requests whose final status is outside 200–299 |
| `product_http_request_duration_seconds` | Histogram | `method`, `endpoint` | Gin middleware request duration, in seconds |

Routes use Gin templates such as `/v1/products/:productId`; raw IDs, request bodies, keys and tokens never become labels. Unknown routes use `unmatched`; nonstandard HTTP methods use `OTHER`. Known series are initialized at zero, allowing an idle graph before any Product traffic.

The histogram has finite boundaries from 10 microseconds through 10 seconds, plus Prometheus's implicit `+Inf` bucket. The dashboard derives p50/p95 with `histogram_quantile`, so these are bucket-based estimates. They can differ from exact percentiles computed from JSON durations. The middleware timer excludes transport, waiting before Gin, and subsequent metric-export/logging overhead; it is not Locust's end-to-end request latency. The [Prometheus histogram documentation](https://prometheus.io/docs/practices/histograms/) explains the aggregation and estimation behavior.

The three main dashboard panels group by method and endpoint:

```promql
# R: completed requests/sec, including 4xx/5xx.
sum by (method, endpoint) (
  rate(product_http_requests_total{job="product",endpoint=~"/v1/products/.*"}[20s])
)

# E: percentage of completed requests that returned non-2xx.
100 * sum by (method, endpoint) (
  rate(product_http_errors_total{job="product",endpoint=~"/v1/products/.*"}[20s])
) / clamp_min(sum by (method, endpoint) (
  rate(product_http_requests_total{job="product",endpoint=~"/v1/products/.*"}[20s])
), 1e-9)

# D: p95 in milliseconds; substitute 0.50 for p50.
histogram_quantile(0.95, sum by (le, method, endpoint) (
  rate(product_http_request_duration_seconds_bucket{job="product",endpoint=~"/v1/products/.*"}[20s])
)) * 1000
```

Top stat panels show aggregate RPS, aggregate error percentage and p50/p95. A phase selector and fixed time ranges label evidence as Idle or Loaded. During idle, RPS and error percentage are zero; duration has **no observations**, so a missing percentile is expected rather than a zero-ms latency measurement. The `clamp_min` denominator displays zero error percentage when there are no requests.

## Run the paired idle/load experiment

```bash
python3 scripts/capture_observability.py \
  --run-id red-my-new-run --idle-seconds 90 --load-seconds 120 --users 200
```

Use a fresh run ID. Avoid other Product requests during the experiment. The collector verifies an idle window, starts two Locust workers with 200 total users and spawn rate 50 users/sec, waits for the configured 120-second run to stop, and captures final scrapes and worker reports. The fixed Grafana URLs are printed and saved in the experiment manifest.

[local_red.py](../locust/local_red.py) uses zero think time and task weights of 80 valid GET, 18 valid POST, 1 missing-product GET, and 1 invalid-details POST. The two negative cases deliberately return 404/400. They count as expected cases in Locust, but remain errors under the assignment's **all non-2xx** RED definition. Consequently, zero unexpected Locust failures and about 2% server RED errors can both be correct. The expected per-endpoint error shares differ: GET is roughly 1/81 and POST roughly 1/19. No panic/fault injection is enabled.

The capture helper saves CSV results, original Prometheus query responses, raw `/metrics` snapshots, a bounded JSON request-log excerpt and a log summary under [evidence/observability](../evidence/observability). It streams the full temporary log **from the beginning** outside Git to survive Docker log rotation, then verifies exact method/route/status counts against the counter deltas. Histogram quantiles remain estimates; JSON log percentiles use nearest rank on durations rounded to microseconds. Local load services stop automatically; the app and monitoring services remain available for inspection.

The first trial, `red-20261016-local`, is explicitly excluded from final evidence because an after-the-fact log read lost early records to Docker log rotation. The completeness check caught this; the verified run uses continuous capture.

## Evidence and screenshots

The verified run completed on 2026-10-16 with 200 users, two workers and a configured 120-second load interval. The dashboard's instant values at the end of each saved phase are:

| Signal | Idle | Loaded, last 20-second rate window |
| --- | ---: | ---: |
| Product requests/sec | 0 | 6,687.24 |
| Product non-2xx percentage | 0% | 1.98% |
| Estimated server p50 | No observations | 0.005197 ms |
| Estimated server p95 | No observations | 0.009875 ms |

Open the real provisioned dashboard at the [fixed Idle window](http://127.0.0.1:13000/d/hw5-product-red/hw5-product-api-red?orgId=1&from=1792178882023&to=1792178947035&var-phase=Idle&kiosk=true) or [fixed Loaded window](http://127.0.0.1:13000/d/hw5-product-red/hw5-product-api-red?orgId=1&from=1792178949000&to=1792179068000&var-phase=Loaded&kiosk=true). The idle image window excludes the initial 25 seconds of the 90-second idle phase, allowing the 20-second rate window to clear previous traffic.

The Evidence phase dropdown changes the annotation only; it does not select a time range or filter measurements. Use the fixed links above. The correct idle range is **12:28:02–12:29:07 America/Los_Angeles**, and the loaded range is **12:29:09–12:31:08**.

The user supplied both original browser screenshots below. The idle image shows zero requests/sec, zero non-2xx percentage, and No traffic for both latency percentiles; no duration samples exist in that window. The loaded image's visible 6.69k requests/sec and 1.98% non-2xx agree with the saved measurements. Both loaded aggregate duration cards round to 0.01 ms at the current display precision.

![Idle Product API RED dashboard, captured by the user](../screenshots/grafana-red-idle.png)

![Loaded Product API RED dashboard, captured by the user](../screenshots/grafana-red-loaded.png)

Locust completed **802,207** samples with **0 unexpected failures**, averaging **6,685.40 RPS**, with client p95 **83 ms**. The server logged **802,407** requests, including **15,945** expected non-2xx responses (**1.9871%**). The extra 200 server records correspond to work still in flight when the 200 virtual users stopped; Locust's sample count and the server's completed-request count have different boundaries. Successful server POSTs always reused the original seeded values.

The [reconciliation result](../evidence/observability/red-20261016-verified-reconciliation.json) confirms exact agreement between logs and request counters, error counters and histogram observation counts. Histogram total duration and rounded JSON total duration also agree within the expected sub-microsecond-per-request rounding tolerance. The exact JSON-log p50/p95 were 0.001/0.008 ms; histogram interpolation in the lowest 0–10 microsecond bucket explains differences from those exact rounded-sample quantiles.

The process used 0.2493 CPU cores near the end of load, about 99.7% of its 0.25-core quota. A [same-run Docker snapshot](../evidence/observability/red-20261016-verified-resources.jsonl) supports that utilization. The much higher Locust p95 includes queueing, client/transport work and scheduling outside the Gin timer; a tiny handler percentile does not mean users experienced microsecond end-to-end responses.

Evidence: [experiment manifest](../evidence/observability/red-20261016-verified-experiment.json), [idle Prometheus queries](../evidence/observability/red-20261016-verified-idle-prometheus.json), [loaded queries](../evidence/observability/red-20261016-verified-loaded-prometheus.json), [request-log excerpt](../evidence/observability/red-20261016-verified-server-excerpt.jsonl), [log summary](../evidence/observability/red-20261016-verified-log-summary.json), [Locust CSV](../evidence/observability/red-20261016-verified_stats.csv), and [collector output](../evidence/observability/verified-experiment-console.txt).

The selected experiment is `red-20261016-verified`. Its manifest contains the exact Idle/Loaded time bounds and Grafana URLs, alongside the corresponding metrics, Locust data and request logs. Both screenshots above are saved unchanged from the user's attachments, with matching time ranges visually verified. Their paths, dimensions and SHA-256 hashes are recorded in the manifest.

Grafana successfully executed all eight dashboard queries through its datasource; [grafana-query-check.json](../evidence/observability/grafana-query-check.json) records that check. Both supplied screenshots verify the visible dashboard layout, including populated charts under load and zero-rate/undefined-latency rendering while idle.

Use the dashboard to notice changes in rate, error ratio and latency across endpoints and time windows. Use structured logs to inspect individual failed or slow requests and to reconstruct what happened in a completed run when no dashboard was open.

## Tests and lifecycle

The Go race test suite passed after instrumentation, with 88.3% statement coverage. [metrics_test.go](../src/metrics_test.go) verifies successful and failed requests, final recovered-500 status, histogram observation counts, bounded route labels, scrape exclusion and independent registries. The local container also passed the existing curl contract checks. Prometheus configuration was validated with `promtool`, and the Terraform mock tests still pass after including `metrics.go` in the source hash. Dockerfile `COPY *.go` includes the new file while `.dockerignore` excludes test sources.

```bash
# Stop load services while preserving the app/dashboard.
scripts/observability.sh stop-load

# Stop the local stack. Named volumes retain the experiment history.
scripts/observability.sh down
```

The current observability code is validated in the local image. The earlier AWS evidence refers to the earlier deployed digest; deploying this version to AWS would require a new Terraform build/apply. Keep `.env.observability`, named-volume databases, temporary full logs and Terraform state out of Git.
