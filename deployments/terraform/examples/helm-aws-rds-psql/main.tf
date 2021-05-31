variable "eks_cluster_name" {
  type = string
}

provider "aws" {
  region = "eu-central-1"
}

data "aws_eks_cluster" "cluster" {
  name = var.eks_cluster_name
}

data "aws_eks_cluster_auth" "cluster" {
  name = var.eks_cluster_name
}

provider "kubernetes" {
  host = data.aws_eks_cluster.cluster.endpoint
  cluster_ca_certificate = base64decode(data.aws_eks_cluster.cluster.certificate_authority.0.data)
  token = data.aws_eks_cluster_auth.cluster.token
}

provider "helm" {
  kubernetes {
    host = data.aws_eks_cluster.cluster.endpoint
    cluster_ca_certificate = base64decode(data.aws_eks_cluster.cluster.certificate_authority.0.data)
    token = data.aws_eks_cluster_auth.cluster.token
  }
}

module "dinonce_rds" {
  source = "../../modules/helm-aws-rds-psql/aws-rds-psql"

  aws_vpc_id = "vpc-xxxxxxxxxxxxxxxxx"

  aws_subnets_rds = [
    "subnet-xxxxxxxxxxxxxxxxx",
    "subnet-xxxxxxxxxxxxxxxxx",
  ]

  aws_security_group_k8s_node = "sg-xxxxxxxxxxxxxxxxx"
}

module "dinonce_helm" {
  source = "../../modules/helm-aws-rds-psql/helm"

  rds_cluster_endpoint = module.dinonce_rds.rds_cluster_endpoint
  rds_cluster_port = module.dinonce_rds.rds_cluster_port
  rds_username = module.dinonce_rds.rds_cluster_username
  rds_password = module.dinonce_rds.rds_cluster_password
  rds_database_name = module.dinonce_rds.rds_cluster_database_name
}
