output "rds_cluster_endpoint" {
  value = module.db.this_rds_cluster_endpoint
}

output "rds_cluster_port" {
  value = module.db.this_rds_cluster_port
}

output "rds_cluster_username" {
  value = module.db.this_rds_cluster_master_username
}

output "rds_cluster_password" {
  value = module.db.this_rds_cluster_master_password
  sensitive = true
}

output "rds_cluster_database_name" {
  value = module.db.this_rds_cluster_database_name
}
