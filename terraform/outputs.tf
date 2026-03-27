output "secret_arns" {
  description = "ARNs of the SSM parameters (populate values manually)"
  value       = { for k, v in aws_ssm_parameter.function_secrets : k => v.arn }
}

output "eventbridge_rules" {
  description = "Fallback EventBridge rules (DISABLED, watchdog enables on k3s failure)"
  value       = { for k, v in aws_cloudwatch_event_rule.fallback : k => v.name }
}
