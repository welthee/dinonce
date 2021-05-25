resource "local_file" "configyaml" {
  filename = "config.yaml"
  content = <<EOF
backendKind: postgres
backendConfig:
  host: localhost
  port: 5432
  user: postgres
  password: postgres
  databaseName: postgres
EOF
}

resource "helm_release" "dinonce" {
  chart = "${path.module}/../../../helm"
  name = var.name
  namespace = kubernetes_namespace.dinonce.metadata.0.name
}