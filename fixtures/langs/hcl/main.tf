terraform {
  required_version = ">= 1.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }

  backend "s3" {
    bucket = "state"
  }
}

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      team = "platform"
    }
  }
}

provider "aws" {
  alias  = "west"
  region = "us-west-2"
}

variable "region" {
  type    = string
  default = "us-east-1"
}

variable "instance_count" {
  type = number

  validation {
    condition     = var.instance_count > 0
    error_message = "must be positive"
  }
}

locals {
  name_prefix = "app-${var.region}"
  common_tags = {
    owner = "ops"
  }
  count_double = var.instance_count * 2
}

data "aws_ami" "ubuntu" {
  most_recent = true

  filter {
    name   = "name"
    values = ["ubuntu/*"]
  }
}

resource "aws_instance" "web" {
  ami           = data.aws_ami.ubuntu.id
  instance_type = "t3.micro"
  tags          = local.common_tags

  ebs_block_device {
    device_name = "/dev/sdf"
  }

  provisioner "local-exec" {
    command = "echo ${self.id}"
  }

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_security_group" "web" {
  name = "${local.name_prefix}-sg"

  ingress {
    from_port = 443
    to_port   = 443
    protocol  = "tcp"
  }

  dynamic "egress" {
    for_each = var.instance_count > 0 ? [1] : []
    content {
      from_port = 0
    }
  }
}

module "network" {
  source = "./modules/network"
  cidr   = "10.0.0.0/16"
}

module "registry_module" {
  source  = "terraform-aws-modules/vpc/aws"
  version = "5.0.0"
}

output "instance_id" {
  value       = aws_instance.web.id
  description = "The id"
}

output "sg_name" {
  value = aws_security_group.web.name
}

moved {
  from = aws_instance.old
  to   = aws_instance.web
}

import {
  to = aws_instance.web
  id = "i-123"
}

check "health" {
  assert {
    condition     = true
    error_message = "x"
  }
}
