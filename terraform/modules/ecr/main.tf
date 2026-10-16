# Create (or ensure) an ECR repo exists
resource "aws_ecr_repository" "this" {
  name = var.repository_name
  # Tags identify source builds; ECS uses the published digest directly.
  image_tag_mutability = "MUTABLE"
  # This repository is dedicated to HW5, including all of its test images.
  force_delete = true
}
