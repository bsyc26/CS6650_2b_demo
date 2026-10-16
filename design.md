# HW5 Design and Tradeoffs

The implemented server is a single process with an in-memory Product store. The design below explains its choices, interprets the measured results, and proposes how to extend the full store API. The proposed database, load balancer, checkout workflow and queue are design work only.

## Product Storage and Request Mix

I expect customers to browse products more often than administrators update product details. I used an 80% GET and 20% POST workload as an explicit test assumption, rather than a measured business distribution. A map keyed by `product_id` provides expected constant-time lookup. A second map from SKU to product ID supports uniqueness checking without scanning the catalog.

Gin serves requests concurrently. `sync.RWMutex` allows concurrent readers and makes each update exclusive. The write lock covers the existence check, SKU uniqueness check, removal of the old SKU, insertion of the new SKU and Product replacement. Locking only the assignment would leave these related operations inconsistent. GET returns a value copy, so callers cannot mutate the stored Product after releasing the lock.

The tradeoff is that every write blocks other readers and writers, even for different products. Both maps consume memory and disappear on restart. Locks synchronize goroutines within one process; they do not synchronize two ECS tasks. Accordingly, Terraform requires one task and stops the old task before starting its replacement. Durable shared storage must come before adding mutable replicas.

The API contract supplies no separate Product creation operation while declaring a POST 404. This implementation seeds three products and treats POST as replacement of an existing record. It documents this interpretation, validates all six required fields, accepts zero weight, and rejects mismatched path/body IDs and duplicate SKUs. A changed business interpretation would require revisiting the store semantics and corresponding tests.

## Client Comparison and Stress Results

These AWS observations use the same Product workload, one 0.25-vCPU/512-MiB Fargate task, and 90-second runs. Both 50-user tests used one worker, a spawn rate of 10 users/sec and a 0.1–0.3-second wait after each task.

| Client and users | Average RPS | Client p95 | Unexpected failures |
| --- | ---: | ---: | ---: |
| HttpUser, 50 | 142.54 | 350 ms | 0 |
| FastHttpUser, 50 | 60.99 | 840 ms | 0 |
| FastHttpUser, 1000 | 650.62 | 3600 ms | 299 of 58568, or 0.5105% |

FastHttpUser was slower in this observed pair. It uses `geventhttpclient` to reduce load-generator CPU overhead; it does not inherently shorten a request's server or network latency. That distinction is explicit in the [Locust performance documentation](https://docs.locust.io/en/2.46.5/increase-performance.html). Sequential public-internet runs do not isolate run order, network variation or connection behavior, so this pair cannot establish a general ranking of the clients.

The 1000-user run used four workers, 100 users/sec ramp-up and zero wait time. It exposed degradation: higher tail latency and transport failures without an HTTP response. The collected one-minute CloudWatch samples did not show CPU saturation. Generator resource measurements collected after stress cannot establish generator headroom during stress. These results demonstrate that the tested request path struggled, but do not establish a precise server capacity limit or its first failing concurrency level.

A follow-up experiment would alternate the clients over repeated runs, keep payloads, pacing, resources and connection policies equal, and collect generator CPU plus server CPU, memory, queues and connection errors throughout. Intermediate concurrency levels would locate the onset of degradation. This is a proposed extension; the completed runs and screenshots remain the recorded observations.

## Local Observability Results

The local experiment uses the app, Prometheus and Grafana in Docker Compose. Prometheus scrapes every five seconds; the dashboard calculates rates over 20 seconds. The separate load generator uses 200 users, two workers, a 50-users/sec ramp and a configured 120-second interval with no think time. Its weights are 80 valid GET, 18 valid POST, one missing-product GET and one invalid POST.

Idle shows zero RPS, zero non-2xx percentage and no duration observations. Under load the final dashboard rate is 6687.24 requests/sec and the non-2xx percentage is 1.98%. The entire run averages 6685.40 Locust requests/sec with zero unexpected failures. Expected 400/404 cases still count as RED errors because RED uses the actual HTTP status; Locust considers those particular test cases successful because they returned the expected response.

The duration histogram measures time within the Gin middleware, excluding network transit, waiting before entering Gin and the later metrics/log-writing overhead. The final estimated aggregate p50/p95 are 0.005197/0.009875 ms, which both display as 0.01 ms. Locust's client p95 is 83 ms. These values describe different timing boundaries; the dashboard does not show complete user-perceived latency. Histogram percentiles are bucket estimates, and percentiles across endpoints must be calculated from combined bucket rates rather than averaged.

The app used about 0.2493 CPU cores near the end of load, approximately 99.7% of its 0.25-core quota. This establishes quota utilization in this local workload, not a portable AWS capacity claim. The local ARM64 environment and Docker networking differ from the public AWS AMD64 path.

The server recorded 802407 completed requests versus 802207 Locust samples. The additional 200 requests correspond to work still in flight at shutdown. Logs agree exactly with request counters, error counters and histogram observation counts. The [observability guide](observability/README.md#evidence-and-screenshots) contains both original dashboard screenshots and the same-run log excerpt.

I use dashboards to spot changes in request rate, error percentage and latency over time. I use structured request logs to inspect specific failures or slow requests and reconstruct an incident after it happened.

## Terraform Declarative Configuration

Declarative configuration describes the desired resources and their relationships. In this project I declare an ECR repository, image publication, a task definition and an ECS service referencing the published digest. Terraform uses providers, state and the dependency graph to plan the operations needed to reach that configuration. An imperative deployment script instead specifies an explicit sequence of commands and must handle already-existing resources and partial completion itself. This distinction follows [HashiCorp's Terraform introduction](https://developer.hashicorp.com/terraform/intro).

This makes changes reviewable in a plan and repeatable on another configured machine. It does not remove failures or make a multi-resource deployment an atomic transaction: a failed apply may leave completed resources in place. Preserve state, inspect the failure and refresh the plan before retrying. The saved no-change plan describes the earlier deployed source; the later local instrumentation changes the build hash and would require a new deployment.

## Scalable Store Proposal

Start with a modular application behind an HTTPS load balancer, multiple replaceable ECS tasks, and a shared PostgreSQL database. Product, Cart, Inventory, Order and Payment are separate ownership boundaries in the code; they do not all need separate deployments initially. Move frequently read Product data into a cache only when measurements justify it. Invalidate affected entries after updates and define a TTL so stale catalog data has a bounded lifetime. The database remains authoritative.

| API area | Storage and correctness decision |
| --- | --- |
| Product GET and details POST | Shared Product table keyed by product ID, unique SKU constraint, validation and version checks to prevent unnoticed concurrent replacement |
| Create cart and add items | Cart and CartItem tables keyed by cart ID and product ID; verify ownership, positive quantity and product existence; apply quantity changes transactionally |
| Cart checkout | Order and OrderItem snapshot tables, a durable checkout state and a unique idempotency record for each logical checkout |
| Warehouse reserve and ship | Inventory and Reservation records; reserve only when sufficient stock exists, and ship only from the corresponding active reservation |
| Payment checkout | Payment attempts and provider transaction identifiers; reuse an idempotency key for retries and reconcile uncertain outcomes with the provider |

Product details may tolerate a briefly stale cache; inventory reservations and payment decisions must use authoritative state. Reserve stock with an atomic conditional update, such as decrementing available quantity only where it is at least the requested quantity. Do not read stock and later decrement it without a transaction or condition. For a multi-item order, either reserve all items within one transaction or explicitly track partial reservations and release them on failure.

At checkout, create a durable order snapshot and reservation before charging. Use trusted server-side pricing; the current Product schema has no price field, so pricing needs an explicit internal model. After a successful charge, mark the order paid and schedule fulfillment. If payment fails, release inventory. If the payment call times out, retain a pending state, query the provider using the same attempt identity, and avoid blindly issuing a second charge. A successful payment followed by a local database failure requires reconciliation or a compensating refund, not an assumption that nothing happened.

If fulfillment is asynchronous, commit the order transition and an outbox record in one database transaction. A relay publishes that record to a queue; consumers deduplicate using a stable event ID before applying side effects. The queue supports buffering and independent worker scaling, while at-least-once delivery still requires idempotent consumers. The [AWS transactional outbox pattern](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html) explains the database/message dual-write problem and duplicate delivery considerations.

The existing warehouse payloads contain product ID and quantity but no order or reservation identity. The contract also lacks checkout status and idempotency fields. A production API would need agreed extensions or versioned endpoints for these identities and uncertain-state lookup; they are not silently assumed to exist in `api.yaml`. Payment data should go directly through the trusted payment boundary and never appear in application logs. Client retries need operation identity, bounded backoff and an explicit timeout policy.

A network partition does not allow a blanket choice of two properties for the entire store. For catalog browsing, serving a known stale cache may be acceptable. For stock reservations or payment confirmation, return an unavailable/pending result when authoritative coordination cannot be completed, rather than promising a sale or successful charge without confirmation.

## Scaling and Failure Tests

After migrating the store, verify that a write through one API task is visible through another and survives task replacement. Test concurrent reservations against the last item, repeated checkout requests with one idempotency key, duplicate fulfillment messages, and a payment timeout after the provider commits. Each case should preserve stock and avoid duplicate charges or shipments.

Then distribute reads across many IDs, add a hot-product workload and increase catalog size. Monitor database lock waits, connection pool saturation, cache hit rate, queue lag, API RED metrics and generator resources. Scale the resource that measurements identify; adding API tasks alone cannot resolve a shared database bottleneck.
