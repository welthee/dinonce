resource "kubernetes_namespace" "dinonce" {
  metadata {
    name = var.name
  }
}

resource "kubernetes_config_map" "dinonce_config" {
  metadata {
    name      = "dinonce-config"
    namespace = kubernetes_namespace.dinonce.metadata.0.name
  }

  data = {
    "config.yaml" = <<EOF
backendKind: postgres
backendConfig:
  host: ${var.rds_cluster_endpoint}
  port: ${var.rds_cluster_port}
  user: ${var.rds_username}
  password: ${var.rds_password}
  databaseName: ${var.rds_database_name}
EOF
  }
}