variable "name" {
  type = string
  default = "dinonce"
}

variable "rds_cluster_endpoint" {
  type = string
}

variable "rds_cluster_port" {
  type = number
}

variable "rds_username" {
  type = string
}

variable "rds_password" {
  type = string
}

variable "rds_database_name" {
  type = string
}