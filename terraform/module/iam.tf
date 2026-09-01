resource "aws_iam_role" "this" {
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action    = "sts:AssumeRole"
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
    }]
  })
  description = local.tags["Description"]
  name        = local.iam_role_name

  tags = merge(
    local.tags,
    {
      Name         = local.iam_role_name
      ResourceType = "IAMRole"
    }
  )
}

resource "aws_iam_role_policy" "this" {
  count = var.slack_delivery_method == "bot_token" ? 1 : 0

  name = local.thread_store_policy_name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Action   = ["dynamodb:GetItem", "dynamodb:PutItem"]
      Effect   = "Allow"
      Resource = aws_dynamodb_table.this[0].arn
    }]
  })

  role = aws_iam_role.this.id
}

resource "aws_iam_role_policy_attachment" "this" {
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
  role       = aws_iam_role.this.name
}
