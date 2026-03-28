# SSM Parameter Store

# k8s function secrets (synced via ESO)

locals {
  secret_names = toset(["from-youtube", "from-discogs", "to-spotify", "manage-playlists"])
}

resource "aws_ssm_parameter" "function_secrets" {
  for_each = local.secret_names
  name     = "/homeplane/${each.key}"
  type     = "SecureString"
  value    = "{}"
  lifecycle { ignore_changes = [value] }
}

# Spotify

resource "aws_ssm_parameter" "spotify_client_id" {
  name  = "/mirrorfm/spotify/client-id"
  type  = "SecureString"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}

resource "aws_ssm_parameter" "spotify_client_secret" {
  name  = "/mirrorfm/spotify/client-secret"
  type  = "SecureString"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}

# Firebase

resource "aws_ssm_parameter" "firebase_project_id" {
  name  = "/mirrorfm/firebase/project-id"
  type  = "String"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}

resource "aws_ssm_parameter" "firebase_api_key" {
  name  = "/mirrorfm/firebase/api-key"
  type  = "String"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}

resource "aws_ssm_parameter" "firebase_auth_domain" {
  name  = "/mirrorfm/firebase/auth-domain"
  type  = "String"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}

# Stripe

resource "aws_ssm_parameter" "stripe_secret_key" {
  name  = "/mirrorfm/stripe/secret-key"
  type  = "SecureString"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}

resource "aws_ssm_parameter" "stripe_webhook_secret" {
  name  = "/mirrorfm/stripe/webhook-secret"
  type  = "SecureString"
  value = "placeholder"
  lifecycle { ignore_changes = [value] }
}
