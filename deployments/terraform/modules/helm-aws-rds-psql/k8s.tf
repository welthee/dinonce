resource "kubernetes_namespace" "dinonce" {
  metadata {
    name = var.name
  }
}

resource "kubernetes_config_map" "dinonce_config" {
  metadata {
    name = "dinonce-config"
    namespace = kubernetes_namespace.dinonce.metadata.0.name
  }

  data = {
    "config.yaml" = <<EOF
backendKind: postgres
backendConfig:
  host: ${module.db.this_rds_cluster_endpoint}
  port: ${module.db.this_rds_cluster_port}
  user: ${module.db.this_rds_cluster_master_username}
  password: ${module.db.this_rds_cluster_master_password}
  databaseName: ${module.db.this_rds_cluster_database_name}
EOF
  }
}