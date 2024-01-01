module "db" {
  source  = "terraform-aws-modules/rds-aurora/aws"
  version = "~> 9.0"

  name           = var.name
  database_name  = var.name
  engine         = "aurora-postgresql"
  engine_version = local.postgres_version
  instance_type  = "db.t3.medium"

  vpc_id  = var.aws_vpc_id
  subnets = var.aws_subnets_rds

  replica_count = 1
  allowed_security_groups = [
  var.aws_security_group_k8s_node]

  storage_encrypted               = true
  apply_immediately               = true
  monitoring_interval             = 10
  db_parameter_group_name         = aws_db_parameter_group.dinonce.name
  db_cluster_parameter_group_name = aws_rds_cluster_parameter_group.dinonce.name

  enabled_cloudwatch_logs_exports = [
  "postgresql"]
}

resource "aws_db_parameter_group" "dinonce" {
  name        = "${var.name}-aur-psql11-paramgroup"
  description = "${var.name}-aur-psql11-paramgroup"
  family      = "aurora-postgresql11"
}

resource "aws_rds_cluster_parameter_group" "dinonce" {
  name        = "${var.name}-aur-psql11-cluster-paramgroup"
  description = "${var.name}-aur-psql11-cluster-paramgroup"
  family      = "aurora-postgresql11"
}
