resource "paperclip_environment" "remote" {
  company_id = paperclip_company.acme.id
  name       = "Remote SSH"
  driver     = "ssh"
  config = jsonencode({
    host                = "ssh.example.com"
    username            = "agent"
    remoteWorkspacePath = "/workspace"
    privateKeySecretRef = { type = "secret_ref", secretId = var.ssh_key_secret_id, version = "latest" }
  })
  env = { EDITOR = { type = "plain", value = "vi" } }
}
