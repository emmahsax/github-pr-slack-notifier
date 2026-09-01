output "function_name" {
  description = "Name of the deployed Lambda function."
  value       = aws_lambda_function.this.function_name
}

output "function_url" {
  description = "Public HTTPS endpoint (auth type NONE — security is enforced inside the Lambda via webhook signature verification, per SEC-002, since GitHub can't do AWS SigV4). Configure this as the GitHub App's webhook URL."
  value       = aws_lambda_function_url.this.function_url
}

output "role_arn" {
  description = "IAM role ARN assumed by the Lambda function."
  value       = aws_iam_role.this.arn
}

output "thread_store_table_name" {
  description = "DynamoDB table grouping a PR's notifications into one Slack thread. Empty string when slack_delivery_method is \"incoming_webhook\", which can't thread."
  value       = var.slack_delivery_method == "bot_token" ? aws_dynamodb_table.this[0].name : ""
}
