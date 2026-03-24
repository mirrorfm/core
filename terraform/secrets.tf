# Secrets management.
# DB credentials: SSM Parameter Store (/mirrorfm/db/*), rotated via scripts/rotate-db-password.py.
# Function-specific secrets: Secrets Manager (homeplane/*), synced to k8s via ESO.
# Lambda env vars: managed outside Terraform (ignore_changes), never in plan output.

# --- Secrets Manager (function-specific secrets, synced to k8s via ESO) ---

locals {
  secret_names = toset(["from-youtube", "from-discogs", "to-spotify", "manage-playlists"])
}

resource "aws_secretsmanager_secret" "function_secrets" {
  for_each = local.secret_names
  name     = "homeplane/${each.key}"
}
