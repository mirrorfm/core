# Secrets management.
# Shared DB credentials stored in SSM Parameter Store (single source of truth).
# Function-specific secrets stored in Secrets Manager (for k8s External Secrets).
# Lambda env vars set from SSM data sources — never in TF plan output.

# --- SSM Parameters (shared DB credentials) ---

data "aws_ssm_parameter" "db_host" {
  name = "/mirrorfm/db/host"
}

data "aws_ssm_parameter" "db_username" {
  name = "/mirrorfm/db/username"
}

data "aws_ssm_parameter" "db_password" {
  name            = "/mirrorfm/db/password"
  with_decryption = true
}

data "aws_ssm_parameter" "db_name" {
  name = "/mirrorfm/db/name"
}

locals {
  db_env = {
    DB_HOST     = data.aws_ssm_parameter.db_host.value
    DB_USERNAME = data.aws_ssm_parameter.db_username.value
    DB_PASSWORD = data.aws_ssm_parameter.db_password.value
    DB_NAME     = data.aws_ssm_parameter.db_name.value
  }
}

# --- Secrets Manager (function-specific secrets, synced to k8s via ESO) ---

locals {
  secret_names = toset(["from-youtube", "from-discogs", "to-spotify", "manage-playlists"])
}

resource "aws_secretsmanager_secret" "function_secrets" {
  for_each = local.secret_names
  name     = "homeplane/${each.key}"
}

# Secret values are managed outside Terraform (via AWS CLI / scripts).
# Terraform only creates the secret resources, not their values.
# This prevents secret values from appearing in TF plan output.
