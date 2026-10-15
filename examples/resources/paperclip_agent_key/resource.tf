ephemeral "paperclip_agent_key" "agent_token" {
  agent_id   = paperclip_agent.engineer.id
  name       = "engineer-key"
  generation = 1
  scope = {
    kind = "standard"
  }
  applying = tofu.applying
}

resource "paperclip_agent_key" "agent_key" {
  agent_id   = paperclip_agent.engineer.id
  name       = "engineer-key"
  generation = 1
  scope = {
    kind = "standard"
  }
}
