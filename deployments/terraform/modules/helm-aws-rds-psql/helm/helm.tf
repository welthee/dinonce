resource "helm_release" "dinonce" {
  chart     = "${path.module}/../../../../helm"
  name      = var.name
  namespace = kubernetes_namespace.dinonce.metadata.0.name
  wait      = false

  set {
    name  = "image.pullPolicy"
    value = "IfNotPresent"
  }

  set {
    name  = "image.tag"
    value = var.release_tag
  }

}
