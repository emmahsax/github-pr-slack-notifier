variable "github_app_id" {
  type = string
}

variable "github_org_allowlist" {
  type = list(string)
}

variable "github_username" {
  type = string
}

variable "slack_delivery_method" {
  type = string
}

variable "slack_target" {
  type    = string
  default = ""
}
