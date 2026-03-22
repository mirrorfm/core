# SQS queues for event-driven processing.
# SNS (from from-github) → SQS → k3s pod (primary) or Lambda (failover).
# Lambda event source mappings are DISABLED by default — homeplane watchdog toggles them.

locals {
  sqs_queues = {
    from-youtube = {
      sns_topic_name = "mirrorfm_incoming_youtube_channel"
      lambda_name    = "mirror-fm_from-youtube"
    }
    from-discogs = {
      sns_topic_name = "mirrorfm_incoming_discogs_label"
      lambda_name    = "mirror-fm_from-discogs"
    }
    to-spotify = {
      sns_topic_name = null # no SNS — fed by from-youtube/from-discogs code
      lambda_name    = "mirror-fm_to-spotify"
    }
  }
}

# --- SQS Queues ---

resource "aws_sqs_queue" "function_queue" {
  for_each                   = local.sqs_queues
  name                       = "mirrorfm-${each.key}"
  visibility_timeout_seconds = 900
  message_retention_seconds  = 1209600 # 14 days
  receive_wait_time_seconds  = 20      # long polling
}

resource "aws_sqs_queue" "function_dlq" {
  for_each                  = local.sqs_queues
  name                      = "mirrorfm-${each.key}-dlq"
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue_redrive_policy" "function_queue" {
  for_each  = local.sqs_queues
  queue_url = aws_sqs_queue.function_queue[each.key].url
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.function_dlq[each.key].arn
    maxReceiveCount     = 3
  })
}

# --- SNS → SQS Subscriptions ---

data "aws_sns_topic" "incoming" {
  for_each = { for k, v in local.sqs_queues : k => v if v.sns_topic_name != null }
  name     = each.value.sns_topic_name
}

resource "aws_sns_topic_subscription" "to_sqs" {
  for_each  = { for k, v in local.sqs_queues : k => v if v.sns_topic_name != null }
  topic_arn = data.aws_sns_topic.incoming[each.key].arn
  protocol  = "sqs"
  endpoint  = aws_sqs_queue.function_queue[each.key].arn
}

resource "aws_sqs_queue_policy" "allow_sns" {
  for_each  = { for k, v in local.sqs_queues : k => v if v.sns_topic_name != null }
  queue_url = aws_sqs_queue.function_queue[each.key].url
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Principal = { Service = "sns.amazonaws.com" }
        Action    = "sqs:SendMessage"
        Resource  = aws_sqs_queue.function_queue[each.key].arn
        Condition = {
          ArnEquals = {
            "aws:SourceArn" = data.aws_sns_topic.incoming[each.key].arn
          }
        }
      }
    ]
  })
}

# --- Lambda Event Source Mappings (DISABLED by default) ---

resource "aws_lambda_event_source_mapping" "sqs_to_lambda" {
  for_each         = local.sqs_queues
  event_source_arn = aws_sqs_queue.function_queue[each.key].arn
  function_name    = each.value.lambda_name
  batch_size       = 1
  enabled          = false

  depends_on = [aws_iam_role_policy.lambda_sqs]
}

# --- IAM: allow Lambda role to interact with SQS ---

resource "aws_iam_role_policy" "lambda_sqs" {
  name = "mirrorfm-sqs-access"
  role = data.aws_iam_role.lambda_role.name
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = [
          "sqs:ReceiveMessage",
          "sqs:DeleteMessage",
          "sqs:GetQueueAttributes",
          "sqs:SendMessage",
        ]
        Resource = concat(
          [for q in aws_sqs_queue.function_queue : q.arn],
          [for q in aws_sqs_queue.function_dlq : q.arn],
        )
      }
    ]
  })
}

# --- Outputs ---

output "sqs_queue_urls" {
  value = { for k, q in aws_sqs_queue.function_queue : k => q.url }
}
