output "cluster_name" {
  description = "ECS cluster name"
  value       = aws_ecs_cluster.this.name
}

output "service_name" {
  description = "ECS service name"
  value       = aws_ecs_service.this.name
}

output "task_definition_arn" {
  value = aws_ecs_task_definition.this.arn
}

output "deployment_configuration" {
  value = {
    image        = var.image
    cpu          = aws_ecs_task_definition.this.cpu
    memory       = aws_ecs_task_definition.this.memory
    architecture = "X86_64"
    task_count   = aws_ecs_service.this.desired_count
    port         = var.container_port
  }
}
