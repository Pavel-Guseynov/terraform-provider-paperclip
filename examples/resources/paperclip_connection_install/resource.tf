resource "paperclip_connection_install" "gitea_install" {
  connection_id = paperclip_connection.gitea_conn.id
  target_type   = "agent"
  agent_ids     = [paperclip_agent.engineer.id]
}
