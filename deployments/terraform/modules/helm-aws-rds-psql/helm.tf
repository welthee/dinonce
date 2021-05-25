resource "local_file" "configyaml" {
  filename = "config.yaml"
  content = <<EOF
backendKind: postgres
backendConfig:
  host: ${module.db.this_rds_cluster_endpoint}
  port: ${module.db.this_rds_cluster_port}
  user: ${module.db.this_rds_cluster_master_username}
  password: ${module.db.this_rds_cluster_master_password}
  databaseName: ${module.db.this_rds_cluster_database_name}
EOF
}

resource "helm_release" "dinonce" {
  chart = "${path.module}/../../../helm"
  name = var.name
  namespace = kubernetes_namespace.dinonce.metadata.0.name
  wait = false

  set {
    name = "configYamlFilePath"
    value = local_file.configyaml.filename
  }

}
