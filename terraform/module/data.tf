data "aws_lambda_function" "this" {
  # Only tier 3 (see locals.lambda_source_code_hash) actually needs this
  # fallback lookup — tier 2 (var.lambda_release.version set) must skip it
  # entirely, or a genuinely first-ever apply using only lambda_release.version
  # (no local build) would still 404 here looking up a function that doesn't
  # exist yet, defeating the whole point of that tier.
  count = local.lambda_zip_exists || var.lambda_release.version != null ? 0 : 1

  function_name = local.lambda_function_name
}
