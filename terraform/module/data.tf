data "aws_lambda_function" "this" {
  count = local.lambda_zip_exists ? 0 : 1

  function_name = local.lambda_function_name
}
