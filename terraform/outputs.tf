output "ecs_cluster_name" {
  description = "Name of the created ECS cluster"
  value       = module.ecs.cluster_name
}

output "ecs_service_name" {
  description = "Name of the running ECS service"
  value       = module.ecs.service_name
}

output "ecr_repository_url" {
  value = module.ecr.repository_url
}

output "image_tag" {
  value = local.image_tag
}

output "cloudwatch_log_group" {
  value = module.logging.log_group_name
}

output "task_definition_arn" {
  value = module.ecs.task_definition_arn
}

output "deployment_configuration" {
  value = module.ecs.deployment_configuration
}

output "aws_region" {
  value = var.aws_region
}
