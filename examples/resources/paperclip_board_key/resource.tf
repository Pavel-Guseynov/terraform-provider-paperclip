ephemeral "paperclip_board_key" "board_token" {
  name       = "automation-board-key"
  generation = 1
  applying   = tofu.applying
}

resource "paperclip_board_key" "board_key" {
  name       = "automation-board-key"
  generation = 1
}
