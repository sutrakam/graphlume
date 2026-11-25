region         = "us-east-1"
instance_count = 3

tags = {
  team = "platform"
}

subnet {
  cidr = "10.0.0.0/24"
}
