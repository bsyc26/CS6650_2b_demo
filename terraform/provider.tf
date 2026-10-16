terraform {
  required_version = ">= 1.9.0, < 2.0.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.7.0"
    }
    docker = {
      source  = "kreuzwerker/docker"
      version = "= 4.6.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      Project   = "cs6650-hw5"
      ManagedBy = "Terraform"
    }
  }
}

data "aws_ecr_authorization_token" "registry" {}

provider "docker" {
  # The helper supplies DOCKER_HOST from the active Docker CLI context.
  host = var.docker_host
  registry_auth {
    address  = data.aws_ecr_authorization_token.registry.proxy_endpoint
    username = data.aws_ecr_authorization_token.registry.user_name
    password = data.aws_ecr_authorization_token.registry.password
  }
}
