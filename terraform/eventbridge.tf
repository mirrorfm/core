# EventBridge fallback rules — DISABLED by default.
# The homeplane watchdog auto-enables these when k3s heartbeat goes stale (>15min).
# The homeplane startup job re-disables them when k3s boots.
#
# Rule naming: homeplane-fallback-* (discovered by prefix, no tags needed).

locals {
  fallback_schedules = {
    from-youtube   = "rate(15 minutes)"
    from-discogs   = "rate(15 minutes)"
    to-spotify     = "rate(10 minutes)"
    sort-playlists = "rate(4 hours)"
  }
}

data "aws_lambda_function" "functions" {
  for_each      = var.lambda_function_names
  function_name = each.value
}

resource "aws_cloudwatch_event_rule" "fallback" {
  for_each            = local.fallback_schedules
  name                = "homeplane-fallback-${each.key}"
  description         = "Fallback: run ${each.key} as Lambda when k3s is offline"
  schedule_expression = each.value
  state               = "DISABLED"
}

resource "aws_cloudwatch_event_target" "fallback" {
  for_each = local.fallback_schedules
  rule     = aws_cloudwatch_event_rule.fallback[each.key].name
  arn      = data.aws_lambda_function.functions[each.key].arn
}

resource "aws_lambda_permission" "fallback" {
  for_each      = local.fallback_schedules
  statement_id  = "AllowEventBridgeFallback"
  action        = "lambda:InvokeFunction"
  function_name = data.aws_lambda_function.functions[each.key].function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.fallback[each.key].arn
}
