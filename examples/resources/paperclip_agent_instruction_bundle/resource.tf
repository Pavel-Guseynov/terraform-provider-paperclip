resource "paperclip_agent_instruction_bundle" "engineer_bundle" {
  agent_id   = paperclip_agent.engineer.id
  mode       = "managed"
  entry_file = "AGENTS.md"
  files = {
    "AGENTS.md" = "# Agent Instructions\n"
  }
}
