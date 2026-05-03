# GitHub webhook ingestion (data repo CSV submissions).
#
# GitHub webhook → Lambda Function URL → enqueue → SQS → Lambda (consumer mode).
#
# Replaces the previous direct API Gateway → Lambda path. The synchronous
# webhook → cold-start container Lambda was occasionally returning 500 when
# the Lambda runtime failed to provision (no log stream, fast 500), and
# GitHub does not retry 5xx — so individual CSV rows could be silently dropped.
# Putting SQS in front gives at-least-once delivery with retry + DLQ. The
# Lambda is also switched from container image to a Go zip package for
# faster, more reliable cold starts.

# --- S3 bucket for Lambda zip artifacts ---

resource "aws_s3_bucket" "lambda_artifacts" {
  bucket = "mirrorfm-lambda-artifacts-${data.aws_caller_identity.current.account_id}"
}

resource "aws_s3_bucket_versioning" "lambda_artifacts" {
  bucket = aws_s3_bucket.lambda_artifacts.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "lambda_artifacts" {
  bucket                  = aws_s3_bucket.lambda_artifacts.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Placeholder zip so the Lambda can be created on first apply.
# CI replaces this with the real build; we ignore_changes so subsequent
# applies don't overwrite the CI-uploaded artifact.
data "archive_file" "from_github_placeholder" {
  type        = "zip"
  output_path = "${path.module}/.from-github-placeholder.zip"
  source {
    content  = "placeholder"
    filename = "bootstrap"
  }
}

resource "aws_s3_object" "from_github_zip" {
  bucket = aws_s3_bucket.lambda_artifacts.id
  key    = "from-github/lambda.zip"
  source = data.archive_file.from_github_placeholder.output_path
  etag   = data.archive_file.from_github_placeholder.output_md5

  lifecycle {
    ignore_changes = [source, etag]
  }
}

# Always reads the latest version (bumped by CI uploads).
data "aws_s3_object" "from_github_zip_latest" {
  bucket     = aws_s3_bucket.lambda_artifacts.id
  key        = aws_s3_object.from_github_zip.key
  depends_on = [aws_s3_object.from_github_zip]
}

# --- SQS queue + DLQ for inbound webhook events ---

resource "aws_sqs_queue" "github_webhook_dlq" {
  name                      = "mirrorfm-from-github-dlq"
  message_retention_seconds = 1209600 # 14 days
}

resource "aws_sqs_queue" "github_webhook" {
  name                       = "mirrorfm-from-github"
  visibility_timeout_seconds = 60 # > Lambda timeout (10s) with margin
  message_retention_seconds  = 1209600
  receive_wait_time_seconds  = 20

  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.github_webhook_dlq.arn
    maxReceiveCount     = 5
  })
}

# --- Webhook secret (HMAC SHA-256 verification) ---
# Standard-tier SSM Parameter Store (String, free). Set the real value after
# first apply, then paste the same value into the GitHub webhook config.
# Lifecycle ignore_changes so TF doesn't overwrite it on subsequent applies.
resource "aws_ssm_parameter" "github_webhook_secret" {
  name  = "/mirrorfm/github/webhook-secret"
  type  = "String"
  value = "REPLACE_ME_AFTER_APPLY"

  lifecycle {
    ignore_changes = [value]
  }
}

data "aws_ssm_parameter" "github_webhook_secret" {
  name = aws_ssm_parameter.github_webhook_secret.name
}

# --- DB credentials (reused by the consumer mode insert) ---

data "aws_ssm_parameter" "from_github_db" {
  for_each        = toset(["db/host", "db/username", "db/password", "db/name"])
  name            = "/mirrorfm/${each.key}"
  with_decryption = true
}

# --- Lambda function (zip, replaces container image variant) ---

resource "aws_lambda_function" "from_github" {
  function_name = "mirror-fm_from-github"
  role          = data.aws_iam_role.lambda_role.arn
  handler       = "bootstrap"
  runtime       = "provided.al2023"
  architectures = ["arm64"]
  memory_size   = 256
  timeout       = 10

  s3_bucket         = aws_s3_bucket.lambda_artifacts.id
  s3_key            = aws_s3_object.from_github_zip.key
  s3_object_version = data.aws_s3_object.from_github_zip_latest.version_id

  environment {
    variables = {
      DB_HOST               = data.aws_ssm_parameter.from_github_db["db/host"].value
      DB_USERNAME           = data.aws_ssm_parameter.from_github_db["db/username"].value
      DB_PASSWORD           = data.aws_ssm_parameter.from_github_db["db/password"].value
      DB_NAME               = data.aws_ssm_parameter.from_github_db["db/name"].value
      GITHUB_WEBHOOK_SECRET = data.aws_ssm_parameter.github_webhook_secret.value
      WEBHOOK_QUEUE_URL     = aws_sqs_queue.github_webhook.url
    }
  }
}

# --- Lambda Function URL (replaces API Gateway) ---

resource "aws_lambda_function_url" "from_github" {
  function_name      = aws_lambda_function.from_github.function_name
  authorization_type = "NONE" # signature verified in handler via webhook secret
}

# Required when authorization_type is NONE: aws_lambda_function_url does not
# auto-create the resource-based policy. Without this, callers (GitHub) get
# 403 Forbidden / AccessDeniedException at the URL.
resource "aws_lambda_permission" "from_github_url" {
  statement_id           = "AllowPublicFunctionURL"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.from_github.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

# --- SQS → Lambda event source mapping (consumer mode) ---

resource "aws_lambda_event_source_mapping" "github_webhook" {
  event_source_arn = aws_sqs_queue.github_webhook.arn
  function_name    = aws_lambda_function.from_github.function_name
  batch_size       = 1
  enabled          = true

  depends_on = [aws_iam_role_policy.lambda_sqs]
}

# --- Outputs ---

output "github_webhook_url" {
  description = "Use this as the GitHub webhook URL in mirrorfm/data settings (replace the old api.execute-api endpoint)."
  value       = aws_lambda_function_url.from_github.function_url
}

output "github_webhook_secret_ssm_path" {
  description = "Set the webhook secret here, then paste the same value into the GitHub webhook config."
  value       = aws_ssm_parameter.github_webhook_secret.name
}
