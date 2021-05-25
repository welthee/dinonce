module "db" {
  source  = "terraform-aws-modules/rds-aurora/aws"
  version = "~> 3.0"

  name           = var.name
  engine         = "aurora-postgresql"
  engine_version = local.postgres_version
  instance_type  = "db.t3.medium"

  vpc_id  = var.aws_vpc_id
  subnets = var.aws_subnets_rds

  replica_count           = 1
  allowed_security_groups = var.aws_security_group_k8s_node

  storage_encrypted   = true
  apply_immediately   = true
  monitoring_interval = 10

  db_parameter_group_name         = "default"
  db_cluster_parameter_group_name = "default"

  enabled_cloudwatch_logs_exports = ["postgresql"]
}
