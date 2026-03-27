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
