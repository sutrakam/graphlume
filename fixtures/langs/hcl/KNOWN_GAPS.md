# HCL: known gaps

A definition is a labelled block at the top level of the file, or an entry of a
top-level `locals` block. The rest is configuration of those and is not indexed.

- **Labelled blocks nested in another block.** Nomad's `group "cache"` and
  `task "redis"` inside `job`, Terraform's `provisioner "local-exec"`,
  `backend "s3"` and `dynamic "egress"`, Packer's `provisioner "shell"` inside
  `build`. All are `block` nodes inside the `body` of a `block`, identical in
  shape to the nested blocks that must not be indexed (`ingress`, `tags`,
  `lifecycle`), and in Terraform the labelled nested ones are never
  definitions. Matching them for Nomad would index every `provisioner` in
  every resource. The cost: a Nomad file contributes its `job` and nothing
  below it.

  ```
  (block (identifier "job") (string_lit) (block_start) (body (block (identifier "group") (string_lit) ...)))
  ```
- **Top-level attributes.** `inputs = { ... }` (Terragrunt), the assignments in
  a `.tfvars` file and `consul = "1.2.3.4"` assign values to something declared
  elsewhere. They are not indexed.
- **Unlabelled blocks.** `terraform`, `build`, `moved`, `import` and a `locals`
  block itself declare nothing nameable (the entries of `locals` are indexed).
- **`resource` and `data` blocks produced by `for_each`, `count` or `dynamic`.**
  One block is one definition, whatever it instantiates.
- **Names are Terraform addresses, built from the block's text.** The two
  labels of `resource "aws_instance" "web"` are two nodes, and a query names one
  node, so the block is captured as both the name and the definition and
  `hclName` rebuilds `aws_instance.web` from its header. A block whose header
  is not `keyword label... {` (a comment between the labels, for example) is
  named by its first line as written; none of the files checked has one.
- **The old HCL 1 `terragrunt = { ... }` syntax** in `terraform.tfvars` parses
  with errors (a block written `config { ... }` inside an attribute value); the
  file is reported partial and declares nothing.
