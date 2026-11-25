# Terragrunt, Packer and Nomad style files share the block syntax.
include "root" {
  path = find_in_parent_folders()
}

dependency "vpc" {
  config_path = "../vpc"

  mock_outputs = {
    vpc_id = "mock"
  }
}

inputs = {
  region = "eu-west-1"
}

source "amazon-ebs" "base" {
  ami_name = "base"
}

build {
  sources = ["source.amazon-ebs.base"]

  provisioner "shell" {
    inline = ["echo hi"]
  }
}

job "cache" {
  group "redis" {
    task "server" {
      driver = "docker"
    }
  }
}

variable "tiny" { type = string }

variable unquoted {
  type = bool
}

locals {
  a = 1
}

locals {
  b = "two"
}

output "script" {
  value = <<EOT
resource "fake" "inside" {
}
EOT
}

resource "null_resource" "count" {
  count = 2
}

# resource "commented" "out" {}
