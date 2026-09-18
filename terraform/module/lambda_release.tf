terraform {
  required_providers {
    http = {
      source  = "hashicorp/http"
      version = ">= 3.3.0"
    }
    local = {
      source  = "hashicorp/local"
      version = ">= 2.0.0"
    }
  }
}

# Second-lowest-priority code source (see locals.lambda_source_code_hash):
# only fetched when var.lambda_release.version is set AND no local build
# exists at var.lambda_zip_path. Downloads the tagged GitHub Release's
# lambda.zip directly — response_body_base64 (not response_body) is
# required here since the zip's raw bytes aren't valid UTF-8, which is all
# an ordinary Terraform string can hold.
data "http" "lambda_release_zip" {
  count = local.lambda_zip_exists || var.lambda_release.version == null ? 0 : 1

  url = "https://github.com/${var.lambda_release.repo}/releases/download/${var.lambda_release.version}/lambda.zip"
}

# Writes the downloaded release zip to local disk so aws_lambda_function's
# `filename` argument (which reads real bytes off disk to upload) has
# something to read. content_base64sha256 gives the exact base64-encoded
# SHA256 AWS's source_code_hash expects, computed by the provider from the
# same bytes being written — no separate re-read of the file is needed (and
# re-reading it via filebase64sha256() wouldn't work here anyway, since that
# function runs at plan time, before this resource has actually written the
# file to disk).
resource "local_file" "lambda_release_zip" {
  count = length(data.http.lambda_release_zip) > 0 ? 1 : 0

  filename       = "${path.module}/.lambda-release/lambda-${var.lambda_release.version}.zip"
  content_base64 = data.http.lambda_release_zip[0].response_body_base64
}
