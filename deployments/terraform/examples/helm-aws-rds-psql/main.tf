
module "dinonce" {
  source = "../../modules/helm-aws-rds-psql"

  aws_vpc_id = ""
  aws_subnets_rds = []

  aws_security_group_k8s_node = ""
}