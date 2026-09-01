variable "function_name" {
  default     = null
  description = "Base logical name for this deployment. Used directly as the Lambda function name, and to derive default names for every other resource (IAM role/policy, DynamoDB table) — override any of those individually via their own *_name variable below if you don't want the derived default. Defaults to \"github-pr-slack-notifier-<github_username>\" so multiple people's instances can coexist in one AWS account without a name collision."
  type        = string
}

variable "github_app_id" {
  description = "The GitHub App's numeric ID."
  type        = string
}

variable "github_app_private_key" {
  description = "The GitHub App's PEM-encoded RSA private key."
  sensitive   = true
  type        = string
}

variable "github_org_allowlist" {
  description = "GitHub organizations this deployment watches (REQ-009). The GitHub App must also be installed on each of these orgs for events to arrive at all."
  type        = list(string)
}

variable "github_username" {
  description = "GitHub login whose subscriptions are evaluated (REQ-001)."
  type        = string
}

variable "github_webhook_secret" {
  description = "The GitHub App's webhook secret, used to verify inbound signatures (SEC-002)."
  sensitive   = true
  type        = string
}

variable "iam_role_name" {
  default     = null
  description = "Name of the Lambda's IAM role. Defaults to PascalCase(function_name) + \"Role\" (e.g. \"GithubPrSlackNotifierRole\"), matching this repo's IAM naming convention."
  type        = string
}

variable "lambda_function_name" {
  default     = null
  description = "Name of the Lambda function. Defaults to function_name as-is."
  type        = string
}

variable "lambda_zip_path" {
  default     = "../../dist/lambda.zip"
  description = "Path to the built Lambda deployment package (run `task build` first). If missing, this module falls back to whatever code is already deployed instead of erroring, so plan/apply is a no-op for anyone who hasn't built it locally — only whoever has the zip can actually push a code update."
  type        = string
}

variable "slack_credential" {
  description = "The Slack bot token or incoming webhook URL, whichever slack_delivery_method selects."
  sensitive   = true
  type        = string
}

variable "slack_delivery_method" {
  description = "Either \"bot_token\" (chat.postMessage, supports grouping a PR's activity into one Slack thread per REQ-015) or \"incoming_webhook\" (fixed-channel webhook, always flat messages per REQ-017 — Slack's incoming webhooks never return a message ts to thread against)."
  type        = string

  validation {
    condition     = contains(["bot_token", "incoming_webhook"], var.slack_delivery_method)
    error_message = "slack_delivery_method must be \"bot_token\" or \"incoming_webhook\"."
  }
}

variable "slack_target" {
  default     = ""
  description = "Slack channel ID (e.g. a dedicated \"my-prs\" channel, with the bot invited) or user ID to DM. Required when slack_delivery_method is \"bot_token\"; unused otherwise (the destination is baked into the webhook URL)."
  type        = string
}

variable "tags" {
  default     = {}
  description = "Tags merged into every resource this module creates. Description/ManagedBy/Owner/Region get sensible defaults automatically (see locals.tf) if not set here, and can be overridden by setting the same key — e.g. tags = { Owner = \"someone-else\", Environment = \"Personal\" }. Name/ResourceType can never be overridden here; they always reflect the actual resource."
  type        = map(string)

  validation {
    # AWS tag values only allow letters/numbers/spaces plus . : / = + - @
    # (IAM's error on an invalid tag value spells out the exact regex) — no
    # commas or other punctuation, and this applies across AWS services,
    # not just IAM, so every value is checked regardless of key.
    condition     = alltrue([for v in values(var.tags) : can(regex("^[\\p{L}\\p{Z}\\p{N}_.:/=+\\-@]*$", v))])
    error_message = "All tag values must only contain characters valid in AWS tags: letters, numbers, spaces, and . : / = + - @ (no commas or other punctuation)."
  }
}

variable "thread_store_policy_name" {
  default     = null
  description = "Name of the IAM inline policy granting the Lambda access to the thread-store DynamoDB table (only created when slack_delivery_method is \"bot_token\"). Defaults to PascalCase(function_name) + \"ThreadStorePolicy\"."
  type        = string
}

variable "thread_store_table_name" {
  default     = null
  description = "Name of the DynamoDB table grouping a PR's notifications into one Slack thread (only created when slack_delivery_method is \"bot_token\"). Defaults to function_name + \"-threads\"."
  type        = string
}
