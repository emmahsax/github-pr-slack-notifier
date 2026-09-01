resource "aws_lambda_function" "this" {
  architectures = ["arm64"]
  description   = local.tags["Description"]

  environment {
    variables = {
      GITHUB_APP_ID           = var.github_app_id
      GITHUB_APP_PRIVATE_KEY  = var.github_app_private_key
      GITHUB_ORG_ALLOWLIST    = join(",", var.github_org_allowlist)
      GITHUB_USERNAME         = var.github_username
      GITHUB_WEBHOOK_SECRET   = var.github_webhook_secret
      SLACK_BOT_TOKEN         = var.slack_delivery_method == "bot_token" ? var.slack_credential : ""
      SLACK_DELIVERY_METHOD   = var.slack_delivery_method
      SLACK_TARGET            = var.slack_target
      SLACK_WEBHOOK_URL       = var.slack_delivery_method == "incoming_webhook" ? var.slack_credential : ""
      THREAD_STORE_TABLE_NAME = var.slack_delivery_method == "bot_token" ? aws_dynamodb_table.this[0].name : ""
    }
  }

  filename      = var.lambda_zip_path
  function_name = local.lambda_function_name
  handler       = "bootstrap"

  # filename's literal string value differs across machines (different local
  # clone paths, or the relative default when no override is given at all)
  # even when the code itself hasn't changed. source_code_hash is the actual
  # signal for whether a real code update is needed; ignoring filename here
  # stops that harmless path drift from ever showing up as a spurious diff.
  lifecycle {
    ignore_changes = [filename]
  }

  memory_size      = 128
  role             = aws_iam_role.this.arn
  runtime          = "provided.al2023"
  source_code_hash = local.lambda_source_code_hash
  timeout          = 30

  tags = merge(
    local.tags,
    {
      Name         = local.lambda_function_name
      ResourceType = "LambdaFunction"
    }
  )
}

resource "aws_lambda_function_url" "this" {
  authorization_type = "NONE"
  function_name      = aws_lambda_function.this.function_name
}
