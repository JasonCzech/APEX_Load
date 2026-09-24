# Each output maps onto a value in deployments/k8s/eks (configmap.yaml,
# serviceaccounts.yaml, kustomization.yaml) — see deployments/k8s/eks/README.md (AWS Terraform) for the wiring.
#
# scripts/render-eks-manifests.sh consumes these names EXACTLY; renaming one
# without updating the script leaves a CHANGE-ME placeholder in the rendered
# manifest, which the script then refuses to emit.

output "aws_region" {
  description = "AWS_REGION and APEXLOAD_AWS_SECRETS_MANAGER_REGION in configmap.yaml."
  value       = var.aws_region
}

output "eks_cluster_name" {
  description = "aws_cluster_name on the EKS cluster connection registered in APEX Load."
  value       = try(aws_eks_cluster.main[0].name, null)
}

output "eks_cluster_endpoint" {
  description = "api_server_url on the EKS cluster connection."
  value       = try(aws_eks_cluster.main[0].endpoint, null)
}

output "eks_oidc_issuer_url" {
  description = "OIDC issuer backing IRSA for the controller and generator service accounts."
  value       = try(aws_eks_cluster.main[0].identity[0].oidc[0].issuer, null)
}

output "ecr_controller_repository_url" {
  description = "kustomization.yaml controller image newName; docker push target."
  value       = aws_ecr_repository.controller.repository_url
}

output "ecr_generator_repository_url" {
  description = "kustomization.yaml generator image newName; docker push target."
  value       = aws_ecr_repository.generator.repository_url
}

output "archive_bucket_name" {
  description = "APEXLOAD_ARCHIVE_BUCKET in configmap.yaml."
  value       = aws_s3_bucket.archives.bucket
}

output "wrapping_kms_key_arn" {
  description = "APEXLOAD_WRAPPING_KMS_KEY_ID in configmap.yaml. The full ARN, never the alias: an alias can be repointed and would strand existing ciphertext."
  value       = aws_kms_key.wrapping.arn
}

output "storage_kms_key_arn" {
  description = "Data-at-rest key for RDS, S3, ElastiCache, and CloudWatch Logs."
  value       = aws_kms_key.storage.arn
}

output "controller_irsa_role_arn" {
  description = "eks.amazonaws.com/role-arn annotation on the controller service account."
  value       = try(aws_iam_role.controller[0].arn, null)
}

output "generator_irsa_role_arn" {
  description = "eks.amazonaws.com/role-arn annotation on the generator service account."
  value       = try(aws_iam_role.generator[0].arn, null)
}

output "efs_file_system_id" {
  description = "ReadWriteMany filesystem for the eks-ha overlay's import-assets PVC. Referenced by the efs-sc StorageClass; unused by the single-controller deployment."
  value       = try(aws_efs_file_system.import_assets[0].id, null)
}

output "generator_node_group_name" {
  description = "The value NodeRef.AgentPool() reports from the eks.amazonaws.com/nodegroup label, and the node pool an allocation pins to."
  value       = try(aws_eks_node_group.generator[0].node_group_name, null)
}

output "database_url_secret_arn" {
  description = "Secrets Manager ARN holding APEXLOAD_DATABASE_URL. Sync it into the apexload-data-platform Secret; it is deliberately not an output value."
  value       = aws_secretsmanager_secret.database_url.arn
}

output "redis_url_secret_arn" {
  description = "Secrets Manager ARN holding APEXLOAD_REDIS_URL."
  value       = aws_secretsmanager_secret.redis_url.arn
}

output "project_secrets_prefix" {
  description = "The Secrets Manager path the controller and generator roles are scoped to."
  value       = local.secret_prefix
}

output "generator_schedulable_pods" {
  description = "How many generator pods the node group can actually schedule. The capacity check compares this against generator_keda_max_replicas."
  value       = local.generator_schedulable_pods
}
