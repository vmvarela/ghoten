terraform {
  required_providers {
    simple = {
      source = "registry.opentofu.org/hashicorp/simple"
    }
  }
}

resource "simple_resource" "test_res" {
  value = "test value"
}

ephemeral "simple_resource" "test_ephemeral" {
  value = "ephemeral value"
}
