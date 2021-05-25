variable "name" {
  type = string
  default = "dinonce"
}

variable "aws_vpc_id" {
  type = string
}

variable "aws_subnets_rds" {
  type = list(string)
}

variable "aws_security_group_k8s_node" {
  type = string
}
