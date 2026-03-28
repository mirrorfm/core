# Secrets management.
# DB credentials: SSM Parameter Store (/mirrorfm/db/*), rotated via scripts/rotate-db-password.py.
# Function-specific secrets: SSM Parameter Store (/homeplane/*), synced to k8s via ESO.
# Lambda env vars: managed outside Terraform (ignore_changes), never in plan output.

# --- SSM Parameter Store (function-specific secrets, synced to k8s via ESO) ---

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

# --- Spotify credentials (read by Lambda at runtime from SSM) ---

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

# --- Firebase config (read by Lambda at runtime + CI for frontend build) ---

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

# --- Stripe config (read by CI workflows from SSM during deploy) ---

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
