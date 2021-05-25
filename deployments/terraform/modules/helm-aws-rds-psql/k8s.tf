resource "kubernetes_namespace" "dinonce" {
  metadata {
    name = var.name
  }

}
