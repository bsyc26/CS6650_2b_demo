variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "ecr_repository_name" {
  type    = string
  default = "cs6650-hw5-product"
}

variable "service_name" {
  type    = string
  default = "cs6650-hw5-product"
}

variable "container_port" {
  type    = number
  default = 8080
  validation {
    condition     = var.container_port >= 1 && var.container_port <= 65535 && floor(var.container_port) == var.container_port
    error_message = "container_port must be an integer TCP port from 1 to 65535."
  }
}

variable "ecs_count" {
  type        = number
  default     = 1
  description = "One task for the mutable in-memory store; replicas do not share updates."
  validation {
    condition     = var.ecs_count == 1
    error_message = "The current mutable in-memory store requires one task. Add shared storage before increasing ecs_count."
  }
}

variable "task_cpu" {
  type    = string
  default = "256"
  validation {
    condition     = contains(["256", "512", "1024"], var.task_cpu)
    error_message = "This homework configuration supports 256, 512 or 1024 CPU units."
  }
}

variable "task_memory" {
  type    = string
  default = "512"
  validation {
    condition = contains(lookup({
      "256"  = ["512", "1024", "2048"]
      "512"  = ["1024", "2048", "3072", "4096"]
      "1024" = ["2048", "3072", "4096", "5120", "6144", "7168", "8192"]
    }, var.task_cpu, []), var.task_memory)
    error_message = "task_memory must form a supported Linux Fargate pair with task_cpu."
  }
}

variable "log_retention_days" {
  type    = number
  default = 7
}

variable "allowed_cidrs" {
  type        = list(string)
  description = "IPv4 CIDRs allowed to call the API, normally the laptop's public address /32."
  validation {
    condition     = length(var.allowed_cidrs) > 0 && alltrue([for cidr in var.allowed_cidrs : can(cidrnetmask(cidr))])
    error_message = "Supply at least one valid IPv4 CIDR."
  }
}

variable "product_api_key" {
  type        = string
  sensitive   = true
  description = "Application API key supplied through TF_VAR_product_api_key, never committed."
  validation {
    condition     = length(var.product_api_key) >= 32 && var.product_api_key != "hw5-local-key"
    error_message = "Use an application API key of at least 32 characters, not the public local demo key."
  }
}

variable "docker_host" {
  type        = string
  default     = null
  nullable    = true
  description = "Optional Docker API socket; otherwise use DOCKER_HOST or the provider default."
}
