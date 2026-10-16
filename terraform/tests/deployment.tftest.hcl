# These plans use mocks only: no AWS account or Docker engine is contacted.
mock_provider "aws" {
  mock_data "aws_vpc" {
    defaults = { id = "vpc-0123456789abcdef0" }
  }
  mock_data "aws_subnets" {
    defaults = { ids = ["subnet-0123456789abcdef0"] }
  }
  mock_data "aws_iam_role" {
    defaults = { arn = "arn:aws:iam::123456789012:role/LabRole" }
  }
  mock_data "aws_ecr_authorization_token" {
    defaults = {
      proxy_endpoint = "https://123456789012.dkr.ecr.us-east-1.amazonaws.com"
      user_name      = "AWS"
      password       = "mock-token"
    }
  }
  mock_resource "aws_ecr_repository" {
    defaults = { repository_url = "123456789012.dkr.ecr.us-east-1.amazonaws.com/cs6650-hw5-product" }
  }
}

mock_provider "docker" {
  mock_resource "docker_registry_image" {
    defaults = { sha256_digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" }
  }
}

variables {
  allowed_cidrs   = ["203.0.113.10/32"]
  product_api_key = "mock-application-key-at-least-32-characters"
}

run "product_deployment_defaults" {
  command = plan
  assert {
    condition     = output.deployment_configuration.task_count == 1
    error_message = "The mutable in-memory Product API must have one task."
  }
  assert {
    condition     = output.deployment_configuration.cpu == "256" && output.deployment_configuration.memory == "512"
    error_message = "The baseline task must use 0.25 vCPU and 512 MiB."
  }
  assert {
    condition     = output.deployment_configuration.port == 8080 && output.deployment_configuration.architecture == "X86_64"
    error_message = "The task port and architecture must match the application image."
  }
  assert {
    condition     = startswith(output.image_tag, "src-") && output.image_tag != "latest"
    error_message = "Application builds must use a content-derived tag."
  }
}

run "reject_independent_memory_replicas" {
  command = plan
  variables { ecs_count = 2 }
  expect_failures = [var.ecs_count]
}

run "reject_invalid_fargate_resource_pair" {
  command = plan
  variables {
    task_cpu    = "512"
    task_memory = "512"
  }
  expect_failures = [var.task_memory]
}

run "reject_public_demo_credential" {
  command = plan
  variables { product_api_key = "hw5-local-key" }
  expect_failures = [var.product_api_key]
}
