# HW5 Mock Interview

先用英文短答，再打开对应代码或证据展开。实现范围是 Product API；数据库、购物车、库存和支付属于设计讨论。

## Opening Answer

> I implemented the Product GET and POST endpoints in Go with Gin, using a synchronized in-memory store. I tested the API natively and in Docker, deployed it to ECS Fargate with Terraform, and compared two Locust clients. I also added local Prometheus RED metrics, Grafana dashboards, and structured request logs. The main limitation is process-local state, so I deliberately run one API task until shared storage is introduced.

## Demo Order

| 时间 | 打开什么 | 讲什么 |
| --- | --- | --- |
| 0–1 分钟 | [api.yaml](api.yaml) 的 Product paths 和 schema | GET 200/404/500，POST 204/400/404/500；全局认证另有 401 |
| 1–3 分钟 | [product-service.go](src/product-service.go) | middleware → auth → validation → store → response；读写锁和 SKU 索引 |
| 3–4 分钟 | [Dockerfile](src/Dockerfile) 和 [Terraform main](terraform/main.tf) | build/runtime 两阶段；ECR 存镜像，ECS 根据 task definition 启动任务 |
| 4–6 分钟 | [Locust 结果](locust/README.md#recorded-results-2026-10-16) | 两客户端对照；1000 用户下的延迟、失败和瓶颈证据边界 |
| 6–8 分钟 | [Grafana 和日志](observability/README.md#evidence-and-screenshots) | Idle/Loaded、RED 口径、预期 4xx、服务端与客户端延迟区别 |
| 8–10 分钟 | [设计回答](design.md#scalable-store-proposal) | 共享数据库、库存原子更新、checkout 幂等和失败恢复 |

## Product API Questions

**How does one request move through the server?**

> Gin matches the route. The outer middleware starts timing, authentication checks the credential, and the handler validates input before calling the store. The handler returns JSON or an empty 204. After the handler finishes, the middleware records the final status, updates RED metrics and emits a JSON log.

GET 的 store 用读锁取 Product 副本；POST 在写锁内完成存在性、SKU 唯一性和替换。Recovery 在计时 middleware 里面，因此恢复出的 500 也能被记录。详细响应例子在 [README](README.md#trigger-every-declared-response)。

**Why a map and RWMutex?**

> A map gives expected constant-time lookup by product ID. RWMutex permits concurrent reads and makes updates atomic across both the Product map and the SKU index.

没有锁会发生数据竞争，并可能触发并发 map 访问错误。锁只保护本进程，不能同步不同容器。所有写共用一把锁也会成为竞争点；不要未经测量就声称 RWMutex 一定比 Mutex 快。

**Why are input fields pointers?**

> Pointers distinguish a missing or null field from a valid zero value. Weight zero is allowed, but an omitted weight is invalid.

**Does POST create a product?**

> In this implementation it replaces an existing product. The specification declares a POST 404 and provides no separate creation endpoint, so I seed known products and document that interpretation.

这是 contract 中需要说明的选择。未知 ID 不能口头说成自动创建；body 的 product_id 必须等于 URL ID，SKU 不能属于其他 Product。

**How did you exercise 500 without making the normal server unreliable?**

> An opt-in local test flag enables a controlled panic header. Recovery converts it to the declared Error response. The flag defaults to false and is explicitly disabled on AWS.

本地 `check_api.py --faults` 对 GET/POST 都验证 500。204 必须无 body；401 来自全局 API key 或 Bearer 认证要求。正常 cloud 测试不发送故障请求。

## Containers and Terraform Questions

**What is the difference between ECR, a task definition and an ECS task?**

> ECR stores images. A task definition describes the image, CPU, memory, port, environment and logging. A task is a running instance of that definition; the ECS service maintains the desired task count.

Fargate 管理底层运行环境，不能说任务都在同一台机器。这里的 AWS service 只有一个 API task。本地 Compose 的 app、Prometheus、Grafana 则共用 Docker Desktop 环境。

**Is this server stateless? Can I scale to two tasks?**

> No. Product updates live in each process's memory. Two replicas would diverge, and restarting loses updates. I must move authoritative state to shared storage before scaling the API tasks.

**What does each Terraform file do?**

> Providers connect Terraform to AWS and Docker. Variables parameterize the deployment. Modules group network, registry, logging and ECS resources. Main connects them, builds and publishes the image, and outputs expose deployment identifiers.

ECS 使用推送后的 image digest，形成 push → task registration 的依赖。`outputs.tf` 不直接保存稳定公网 IP；helper 查询运行任务的 ENI。

**What does declarative mean? Is apply atomic?**

> I declare the desired resources and references; Terraform plans the changes and orders them using dependencies. Apply is not one atomic transaction: it can fail after creating some resources, so state and a refreshed plan matter for recovery.

不把 state、plan、tfvars、`.env` 或私钥提交 Git。`.terraform.lock.hcl` 则应保留。后加的本地 metrics 代码没有自动出现在此前 AWS 镜像里。

## Load Test Questions

**What was your test design?**

> The AWS baseline used 50 users, one worker, a 10-users-per-second ramp and 0.1–0.3 seconds of wait, with an 80/20 GET/POST mix. Stress used 1000 users, four workers, a 100-users-per-second ramp and no wait for 90 seconds.

这是重复访问同一热门 Product，不代表全目录。虚拟用户、RPS、worker 是三个概念：用户数是并发行为模型，worker 是压测进程数，RPS 是实际完成速率。

**Was FastHttpUser faster? Why?**

> In my observed pair, HttpUser achieved 142.54 RPS and FastHttpUser 60.99 RPS. FastHttpUser reduces generator CPU overhead, but does not inherently reduce individual request latency. I need repeated alternating runs to attribute this difference.

这两次都是零失败，但单次公网实验不能下普遍结论。解释和官方文档见 [设计回答](design.md#client-comparison-and-stress-results)。

**Where did the server start struggling?**

> At the tested 1000-user level, client p95 reached 3.6 seconds and 299 of 58568 requests failed without an HTTP response. This establishes degradation in the tested path, but I did not isolate the exact onset or root cause.

CloudWatch 样本未显示 AWS CPU 饱和，压测结束后的 generator snapshot 也不能证明运行时有余量。下一步检查持续 generator 资源、连接错误、服务端队列，并补中间并发档位。不要把记录里的 status 0 当作 HTTP 状态码。

## Observability Questions

**What are RED and why are there errors with zero Locust failures?**

> Rate is completed requests per second, Errors is the non-2xx percentage, and Duration is the request latency distribution. The local workload deliberately expects some 400 and 404 responses. They are successful test cases in Locust but remain errors under the dashboard's HTTP definition.

本地 200 users / 2 workers / 120 秒，约 6685 平均 RPS；截图最后窗口 6.69k RPS 和 1.98% 非 2xx。Idle 的延迟是 No traffic，不是测出了 0 ms。

**Why does Grafana show 0.01 ms while Locust p95 is 83 ms?**

> The histogram measures time inside Gin, while Locust measures the client request path. Queueing before Gin, transport and other work outside the timer can dominate. Histogram quantiles are also bucket estimates, rounded on the cards.

不能将 endpoint 的 p95 直接平均；要先合并 histogram buckets 的速率，再算 quantile。每个 endpoint 有独立 RED 曲线，URL 使用路由模板，不把真实 product ID 作为 label。

**How do you know the logs belong to the same run?**

> The experiment records phase times and captures logs throughout the run. Its 802407 server completions match the request counters and histogram counts, and its 15945 non-2xx log records match the error counter.

Locust 有 802207 samples，停测时另有 200 个在途请求完成。看 [同一轮日志摘录](evidence/observability/red-20261016-verified-server-excerpt.jsonl) 的 method、endpoint、status、duration_ms；日志不含 API key。

**Dashboard or logs?**

> The dashboard shows trends and which endpoint changed. Logs show individual request outcomes and support investigation after the incident.

## System Design Questions

**How would you scale the whole store?**

> Put replaceable API tasks behind a load balancer and move authoritative data to shared storage. Cache catalog reads, use atomic inventory reservations, persist checkout state and idempotency records, and process fulfillment through durable events when asynchronous work is useful.

先共享存储，再加 task；不要一开始就为每个 endpoint 拆一个服务。Product schema 没有价格，warehouse API 没有 reservation ID；这些生产设计需要补充 contract，不能说原 API 已经支持。

**A payment times out. Should you retry?**

> A timeout does not prove the charge failed. I keep the order pending, reuse the same payment identity, and query or reconcile with the provider before deciding what to retry.

库存不足必须原子拒绝；重复 checkout、重复消息不能重复扣款或发货。具体状态和 outbox 设计见 [设计回答](design.md#scalable-store-proposal)。

## Figures to Remember

| 场景 | 最重要的数字 | 能说明什么 |
| --- | --- | --- |
| AWS HttpUser 50 | 142.54 RPS，p95 350 ms | 一次基线观测 |
| AWS FastHttpUser 50 | 60.99 RPS，p95 840 ms | 不能宣称 Fast 总更快 |
| AWS stress 1000 | 650.62 RPS，0.5105% 失败，p95 3.6 s | 请求路径出现退化，未定位根因 |
| Local RED loaded | 平均 6685.40 RPS，约 1.99% 预期 4xx，client p95 83 ms | 本地指标与日志对齐；不与公网 AWS 直接排名 |
| Local app CPU | 0.2493 cores / 0.25 quota | 此本地负载接近用满 CPU quota |

演示前启动 Docker；本地监控用 `scripts/observability.sh up`。只有准备现场重跑云端实验时才需要 Learner Lab 和有效 AWS 凭据。已保存的截图、CSV 和日志可直接用于 mock。
