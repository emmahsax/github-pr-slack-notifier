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

# Every tier below mirrors its winning bytes to this same fixed,
# machine-independent path — path.module resolves to the same relative
# string for a given module source/ref regardless of which machine or
# runner applies it — rather than aws_lambda_function referencing
# var.lambda_source.zip_path (an arbitrary, machine-specific path) or a
# per-version release path directly.
#
# This matters because the AWS provider only re-reads and re-uploads the
# code when `filename` OR `source_code_hash` differs from state (see
# needsFunctionCodeUpdate in terraform-provider-aws' lambda/function.go).
# `lifecycle.ignore_changes` on `filename` is not a viable substitute for
# this: it freezes `filename` to whatever value is currently recorded in
# state, permanently, regardless of which tier is active — blocking every
# real deploy once that frozen value stops being a readable file, since the
# provider still tries to re-read it for any update to the resource, for
# any reason at all. Routing every tier through this one fixed path avoids
# that problem at its root: a tier-3 apply (no local build, no
# lambda_source.github_release_version) computes this exact same path with no local_file
# behind it and a source_code_hash matching what's already deployed, so it
# never triggers a real read, whether or not the file exists on disk.
locals {
  lambda_deploy_zip_path = "${path.module}/.lambda-deploy/lambda.zip"
}

# Tier 1 (see locals.lambda_source_code_hash): mirrors a real local build
# (var.lambda_source.zip_path) to lambda_deploy_zip_path, so it's
# indistinguishable from a tier 2 release download by the time
# aws_lambda_function sees it.
resource "local_file" "lambda_zip_copy" {
  count = local.lambda_zip_exists ? 1 : 0

  filename       = local.lambda_deploy_zip_path
  content_base64 = filebase64(var.lambda_source.zip_path)
}

# Tier 2: only fetched when var.lambda_source.github_release_version is set AND no local
# build exists at var.lambda_source.zip_path. Downloads the tagged GitHub
# Release's lambda.zip directly — response_body_base64 (not response_body)
# is required here since the zip's raw bytes aren't valid UTF-8, which is
# all an ordinary Terraform string can hold.
data "http" "lambda_release_zip" {
  count = local.lambda_zip_exists || var.lambda_source.github_release_version == null ? 0 : 1

  url = "https://github.com/${var.lambda_source.github_release_repo}/releases/download/${var.lambda_source.github_release_version}/lambda.zip"
}

resource "local_file" "lambda_release_zip" {
  count = length(data.http.lambda_release_zip) > 0 ? 1 : 0

  filename       = local.lambda_deploy_zip_path
  content_base64 = data.http.lambda_release_zip[0].response_body_base64
}
