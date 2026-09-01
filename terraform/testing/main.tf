# Scratch harness for testing the module locally before tagging a release
# and wiring up the "real" thin consumer block elsewhere. Not part of the
# published module itself — this config, variables.tf, and the .example
# tfvars are committed since they hold no secrets, but the real
# terraform.tfvars and all local state stay gitignored (see root .gitignore).
#
# Usage:
#   1. `task build` from the repo root (produces ../../dist/lambda.zip).
#   2. Fill in terraform.tfvars (copy terraform.tfvars.example).
#   3. Make sure your AWS credentials/region are set (env vars or ~/.aws
#      config) for whichever account you're testing against.
#   4. terraform init && terraform plan && terraform apply — run these
#      yourself; nothing here should run non-interactively.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {}

# The module takes secret values directly, not backend-specific
# references — this harness resolves them from SSM itself, prefixed with
# github_username to keep instances distinct if ever shared in one account.
data "aws_ssm_parameter" "github_app_private_key" {
  name            = "/github-pr-slack-notifier/${var.github_username}/github-app-private-key"
  with_decryption = true
}

data "aws_ssm_parameter" "github_webhook_secret" {
  name            = "/github-pr-slack-notifier/${var.github_username}/github-webhook-secret"
  with_decryption = true
}

data "aws_ssm_parameter" "slack_credential" {
  name            = "/github-pr-slack-notifier/${var.github_username}/slack-credential"
  with_decryption = true
}

module "pr_slack_notifier" {
  source = "../module"

  github_app_id          = var.github_app_id
  github_app_private_key = data.aws_ssm_parameter.github_app_private_key.value
  github_org_allowlist   = var.github_org_allowlist
  github_username        = var.github_username
  github_webhook_secret  = data.aws_ssm_parameter.github_webhook_secret.value
  slack_credential       = data.aws_ssm_parameter.slack_credential.value
  slack_delivery_method  = var.slack_delivery_method
  slack_target           = var.slack_target
}

output "function_url" {
  value = module.pr_slack_notifier.function_url
}

output "function_name" {
  value = module.pr_slack_notifier.function_name
}

output "thread_store_table_name" {
  value = module.pr_slack_notifier.thread_store_table_name
}
