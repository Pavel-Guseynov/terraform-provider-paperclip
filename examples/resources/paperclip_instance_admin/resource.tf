resource "paperclip_instance_admin" "admin" {
  user_id           = data.paperclip_user.operator.id
  is_instance_admin = true
}
