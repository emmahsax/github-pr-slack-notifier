# Groups a PR's notifications into a single Slack thread (REQ-015),
# threading only being possible with bot_token delivery (REQ-017). TTL
# bounds table growth automatically — see internal/threadstore for the
# attribute names this must match.
resource "aws_dynamodb_table" "this" {
  count = var.slack_delivery_method == "bot_token" ? 1 : 0

  attribute {
    name = "pr_key"
    type = "S"
  }
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pr_key"
  name         = local.thread_store_table_name

  tags = merge(
    local.tags,
    {
      Name         = local.thread_store_table_name
      ResourceType = "DynamoDBTable"
    }
  )

  ttl {
    attribute_name = "expires_at"
    enabled        = true
  }
}
