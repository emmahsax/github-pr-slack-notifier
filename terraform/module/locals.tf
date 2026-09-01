# This module takes secret VALUES directly (github_app_private_key,
# github_webhook_secret, slack_credential — all sensitive) rather than
# reading them from any specific backend itself. That keeps the module
# agnostic to how a consumer sources/stores plaintext secrets — SSM
# Parameter Store, SOPS-encrypted tfvars, Vault, whatever — it's the
# consumer's job to resolve a value and pass it in. The Lambda's own IAM
# role never needs any secrets-backend read permission either way. These
# values do land in Terraform state in plaintext, same as any other
# resource attribute — restrict state storage access accordingly.

locals {
  # function_name defaults to include github_username so multiple people's
  # instances of this module can coexist in one AWS account without a name
  # collision — override var.function_name directly if you don't want that.
  function_name = coalesce(var.function_name, "github-pr-slack-notifier-${var.github_username}")

  # Sensible defaults so a shared/employer account deployment is
  # self-describing out of the box (GUD-004) without hand-writing ownership
  # prose — var.tags can override any of these (validated for AWS-safe tag
  # characters, see variables.tf) by setting the same key. Only Name/
  # ResourceType (set per-resource, not here) are never overridable, since
  # they must always match the actual resource. Region is informational
  # only: it does NOT configure this module's AWS provider (a child module
  # declaring its own provider would break using count/for_each on it at the
  # call site, which multi-instance deployments need) — actual region is
  # whatever the caller's default provider uses.
  default_tags = {
    # "--" instead of ", " deliberately: AWS tag values only allow
    # letters/numbers/spaces plus . : / = + - @ (IAM's error on an invalid
    # tag value spells out the exact regex) — no commas.
    Description = "GitHub PR-notification Lambda for ${var.github_username} -- safe to destroy on offboarding -- code at github.com/${var.github_username}/github-pr-slack-notifier"
    ManagedBy   = "IAC"
    Owner       = var.github_username
    Region      = "us-east-2"
  }
  tags = merge(local.default_tags, var.tags)

  # IAM resource names are PascalCase (e.g. "TerrateamRole", "CustomEC2Policy")
  # while everything else (Lambda, DynamoDB, general resource Name tags) is
  # dash-case. local.function_name is the one dash-case logical name every resource
  # name defaults from, so this converts it to PascalCase for the IAM-only
  # defaults below rather than asking consumers to type a second name just
  # to get the default right — var.iam_role_name/var.thread_store_policy_name
  # still let a consumer override it directly if they want something else.
  pascal_function_name = join("", [for part in split("-", local.function_name) : title(part)])

  # Every AWS resource name below has an explicit override variable
  # (default null) that falls back to a sensible name derived from
  # local.function_name when unset. IAM names include github_username via
  # local.function_name's own default, same reasoning as above.
  lambda_function_name     = coalesce(var.lambda_function_name, local.function_name)
  iam_role_name            = coalesce(var.iam_role_name, "${local.pascal_function_name}Role")
  thread_store_policy_name = coalesce(var.thread_store_policy_name, "${local.pascal_function_name}ThreadStorePolicy")
  thread_store_table_name  = coalesce(var.thread_store_table_name, "${local.function_name}-threads")

  # dist/lambda.zip is gitignored and only ever exists on whoever's machine
  # last ran `task build` — it will NOT be present for most people who plan
  # or apply this same state. When it's missing, fall back to whatever hash
  # is already deployed so this resource is a silent no-op for them; only
  # the person who actually rebuilt the binary sees (and can apply) a real
  # code diff. This only fails on a first-ever apply of this module from a
  # machine without the local zip, since there's no existing function yet
  # to fall back to — the very first apply must come from whoever has it.
  lambda_zip_exists       = fileexists(var.lambda_zip_path)
  lambda_source_code_hash = local.lambda_zip_exists ? filebase64sha256(var.lambda_zip_path) : try(data.aws_lambda_function.this[0].code_sha256, null)
}
