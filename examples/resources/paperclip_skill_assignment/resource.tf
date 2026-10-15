resource "paperclip_skill_assignment" "engineer_skills" {
  agent_id = paperclip_agent.engineer.id
  skills = [
    {
      key        = paperclip_skill.task_lifecycle.key
      version_id = paperclip_skill.task_lifecycle.current_version_id
    }
  ]
}
