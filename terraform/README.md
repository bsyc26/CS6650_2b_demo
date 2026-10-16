# Product API Terraform Deployment

This configuration adapts the assignment's [CS6650 starter](https://github.com/RuidiH/CS6650_2b_demo) at commit `54246e1fc8caf99cdda879da33b7b5dee3e27b48` to deploy the Product API in `../src`. It builds and publishes the image, creates one ECS Fargate service, and sends its JSON request logs to CloudWatch. Deployment requires an active Learner Lab session and temporary AWS credentials.

## Files and dependencies

| File or module | Responsibility |
| --- | --- |
| `provider.tf` | AWS region and tags, pinned AWS/Docker providers, ECR authentication |
| `variables.tf` | Service names, port, caller CIDRs, task resources and application credential |
| `main.tf` | Connects modules, hashes build inputs, builds and pushes the image |
| `modules/network` | Uses default VPC subnets and creates a dedicated security group |
| `modules/ecr` | Creates the dedicated Product image repository |
| `modules/logging` | Creates a CloudWatch log group with seven-day retention |
| `modules/ecs` | Creates cluster, task definition and one Fargate service |
| `outputs.tf` | Exposes service identifiers, published image and task configuration |
| `.terraform.lock.hcl` | Records provider versions and checksums; commit this file |
| `tests/deployment.tftest.hcl` | Uses mocks to check defaults and reject unsafe configuration combinations |

The source follows the starter's four-module structure. The starter server and Dockerfile are replaced by the Product API and its tested multi-stage Dockerfile. The Docker provider is updated to 4.6.0 for the current Docker/Buildx workflow; AWS remains pinned to the starter's 6.7.0.

The dependency chain is:

```text
create ECR -> build Linux/AMD64 image -> push image -> register task definition
                                                             |
default subnet + security group + log group + LabRole ---------+
                                                             |
                                                      start ECS service
```

ECS references `repository@sha256:digest`, so task registration depends on the completed push. A hash of the build inputs changes the image tag when code, dependencies or Docker settings change. The repository permits tag replacement, while each task is pinned to the published digest. No application key is included in the image.

## Local prerequisites

- AWS CLI v2, Terraform 1.9 or newer, Docker with Buildx, Python 3.9 or newer, and curl.
- A running Docker engine and an active Learner Lab session.
- `LabRole` and a default VPC with public default subnets in `us-east-1`, matching the current Learner Lab region.
- The required starter fork is [bsyc26/CS6650_2b_demo](https://github.com/bsyc26/CS6650_2b_demo), created on 2026-10-16. Its [recorded metadata](../evidence/github-starter-fork.json) confirms `RuidiH/CS6650_2b_demo` as its parent. This fork replaces the starter server with the Product implementation in `src/` and retains the `terraform/` module layout. The upstream source commit is recorded above.

On the current Mac, Terraform and AWS CLI are installed under `~/.local/bin`; Docker Desktop provides Docker under `/Applications/Docker.app/Contents/Resources/bin`. The wrapper resolves the active Docker CLI context so the provider uses the same socket on macOS and Linux.

## Configure Learner Lab credentials

Start the lab, wait for its ready indicator, and open AWS Details. From the repository root, run this in your own terminal:

```bash
python3 scripts/configure_aws.py
AWS_PROFILE=learner-lab aws sts get-caller-identity
```

The script asks for access key, secret key and session token with hidden prompts, then writes profile `learner-lab` to the standard AWS files with private file permissions. Other profiles are preserved. It selects `us-east-1`. Refresh these temporary credentials when the lab session changes or they expire. Credentials entered only in another terminal's environment are not inherited by an already-running Codex process; the named profile works across terminals.

No AWS keys or tokens belong in Terraform source, variable files, screenshots or chat. The application API key described below is a separate credential.

## Prepare the application key and caller address

From the repository root:

```bash
python3 scripts/prepare_cloud.py
```

This creates two ignored local files:

- `.env.aws`: a newly generated application API key, kept across redeployments.
- `terraform/client.auto.tfvars.json`: the current public IPv4 address from `checkip.amazonaws.com`, with `/32` for inbound API access.

If your network or VPN changes, refresh the address and apply the resulting security group change:

```bash
python3 scripts/prepare_cloud.py --refresh-network
```

The wrapper loads the application key through `TF_VAR_product_api_key` and defaults to AWS profile `learner-lab`. Set `AWS_PROFILE` explicitly to use another configured profile. Other settings can be customized using an ignored `terraform.tfvars` based on [the example](terraform.tfvars.example).

## Initialize plan and deploy

From the repository root:

```bash
scripts/terraform.sh init
scripts/terraform.sh fmt -check -recursive
scripts/terraform.sh validate
scripts/terraform.sh plan -out=deployment.tfplan
# Review the plan, then apply exactly that saved plan.
scripts/terraform.sh apply deployment.tfplan
```

The initial infrastructure contains the dedicated ECR repository, security group, CloudWatch log group, ECS cluster, task definition and ECS service, plus local Docker image/push resources. Existing LabRole, default VPC and subnets are data sources, not resources to create or delete.

Defaults are one Linux/X86_64 Fargate task with 0.25 vCPU, 512 MiB, TCP port 8080 and a public IP. The security group allows that port only from the configured caller CIDRs. No load balancer or NAT gateway is created. `PORT` and `PRODUCT_API_KEY` are injected at runtime, and `PRODUCT_TEST_FAULTS` is explicitly false.

The mutable in-memory store requires one task. The configuration rejects a larger desired count until shared storage is implemented. During replacement, the old task stops before the new one starts, trading a brief interruption for avoiding simultaneous divergent replicas. A restart still resets product data. The ECS deployment circuit breaker can roll back failed deployments; Terraform waits for the service to stabilize. Application HTTP readiness is checked separately below because the distroless image has no shell/curl health-check command.

## Find and verify the API

After a successful apply:

```bash
BASE_URL=$(python3 scripts/ecs_endpoint.py)
curl --fail --max-time 15 "${BASE_URL%/v1}/health"
set -a
. ./.env.aws
set +a
python3 scripts/check_api.py --base-url "$BASE_URL"
```

The endpoint helper reads Terraform outputs, resolves the service's running task through ECS, then reads its ENI's public IP through EC2. Resolve it again after a deployment or task replacement. The same application checks used locally verify real cloud responses and restore the modified product. Cloud checks omit `--faults`, since the controlled 500 switch is disabled there; 500 paths were exercised in the local tests.

View request logs using the dedicated group:

```bash
AWS_PROFILE=learner-lab aws logs tail /ecs/cs6650-hw5-product \
  --region us-east-1 --since 10m
```

If a task does not start, inspect ECS service events and stopped task reasons before retrying. An HTTP timeout can indicate a changed client IP, missing subnet internet route, or unfinished startup. An authentication error can indicate that the application key differs from the one injected by Terraform.

## Local checks without AWS resources

```bash
scripts/terraform.sh init -backend=false
scripts/terraform.sh validate
scripts/terraform.sh test
```

The test file mocks both providers. It checks the baseline architecture/resources, content-based tags, rejection of multiple independent memory replicas, invalid Fargate CPU/memory pairs, and the public demo credential. Passing these tests checks configuration, not real AWS permissions or task startup. Live deployment results must be recorded separately.

## Verified AWS deployment

On 2026-10-16, the live plan and apply succeeded in `us-east-1`: eight managed resources were added, with no existing resources modified or destroyed. ECS reached a steady state with deployment `COMPLETED`, one running task and zero pending. The task uses image digest `sha256:effab76693b894411a3b6c91386be47d5a4a5fe95b0075297de0612a18319b12` and task definition `cs6650-hw5-product-task:1`.

The public health endpoint returned `{"status":"ok"}`. Real GET/POST contract checks, read-after-write, authentication and restoration passed. CloudWatch received 22 structured request log entries in the collected excerpt. Cloud fault injection stayed disabled.

Evidence: [plan](../evidence/aws-plan.txt), [apply](../evidence/aws-apply.txt), [ECS status](../evidence/aws-service-status.json), [running task and image digest](../evidence/aws-task.json), [HTTP checks](../evidence/aws-http-checks.txt), [request logs](../evidence/aws-server.jsonl), and [screenshot](../screenshots/aws-response-codes.jpg). A subsequent [plan found no changes](../evidence/aws-no-changes.txt). The API address at verification time is recorded in [aws-endpoint.txt](../evidence/aws-endpoint.txt); resolve it again after task replacement.

## State and cleanup

Keep `.terraform/`, state files, saved plans, local variables and `.env.aws` out of Git. Sensitive marking redacts normal Terraform output; the local state and saved plans can still contain application credentials and registry authorization data. The wrapper sets a private file-creation mask for these files. Preserve the state until resources have been destroyed. Keep the provider lock file in Git. The recorded lab deployment's private state remains in the original local checkout. A fresh clone does not adopt those running resources; use the original checkout to manage them, or deliberately migrate state through a protected channel before switching. No state or credentials are included in this repository.

After collecting the required evidence and finishing tests:

```bash
scripts/terraform.sh plan -destroy -out=destroy.tfplan
# Review that only this homework's resources will be removed.
scripts/terraform.sh apply destroy.tfplan
```

The dedicated ECR repository removes its own images on destroy. Default networking and the pre-existing LabRole remain. Destroy also needs valid lab credentials and the same state; stopping the lab is not a substitute for recording successful Terraform cleanup.
