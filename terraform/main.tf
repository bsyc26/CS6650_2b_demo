# Adapted from RuidiH/CS6650_2b_demo, commit
# 54246e1fc8caf99cdda879da33b7b5dee3e27b48.
locals {
  build_files = ["Dockerfile", ".dockerignore", "go.mod", "go.sum", "product-service.go", "metrics.go"]
  source_hash = sha256(join("", [for file in local.build_files : filesha256("${path.module}/../src/${file}")]))
  image_tag   = "src-${substr(local.source_hash, 0, 20)}"
}

module "network" {
  source         = "./modules/network"
  service_name   = var.service_name
  container_port = var.container_port
  cidr_blocks    = var.allowed_cidrs
}

module "ecr" {
  source          = "./modules/ecr"
  repository_name = var.ecr_repository_name
}

module "logging" {
  source            = "./modules/logging"
  service_name      = var.service_name
  retention_in_days = var.log_retention_days
}

data "aws_iam_role" "lab_role" {
  name = "LabRole"
}

resource "docker_image" "app" {
  name         = "${module.ecr.repository_url}:${local.image_tag}"
  keep_locally = true
  build {
    context    = abspath("${path.module}/../src")
    dockerfile = "Dockerfile"
    platform   = "linux/amd64"
    builder    = "default"
    provenance = "false"
  }
  triggers = { source_hash = local.source_hash }
}

resource "docker_registry_image" "app" {
  name = docker_image.app.name
  # The dedicated AWS ECR repository handles remote image cleanup on destroy.
  keep_remotely = true
}

module "ecs" {
  source       = "./modules/ecs"
  service_name = var.service_name
  # A pushed digest makes publication a dependency of task registration and
  # pins tasks to the exact published image rather than a moving latest tag.
  image              = "${module.ecr.repository_url}@${docker_registry_image.app.sha256_digest}"
  container_port     = var.container_port
  subnet_ids         = module.network.subnet_ids
  security_group_ids = [module.network.security_group_id]
  execution_role_arn = data.aws_iam_role.lab_role.arn
  task_role_arn      = data.aws_iam_role.lab_role.arn
  log_group_name     = module.logging.log_group_name
  ecs_count          = var.ecs_count
  region             = var.aws_region
  cpu                = var.task_cpu
  memory             = var.task_memory
  product_api_key    = var.product_api_key
}
