terraform {
  required_version = "~> 1.0"
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes"
      version = "~> 2.2"
    }
    helm = {
      source = "hashicorp/helm"
      version = "~> 2.1"
    }
  }
}
