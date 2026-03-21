# AWS Secrets Manager secrets for k3s workers.
# External Secrets Operator syncs these into k8s Secrets.
#
# Values come from TF variables (set via TF_VAR_* env vars in GHA,
# sourced from GitHub Actions secrets). Never hardcoded in code.

variable "secret_from_youtube" {
  description = "JSON string of env vars for from-youtube"
  type        = string
  sensitive   = true
}

variable "secret_from_discogs" {
  description = "JSON string of env vars for from-discogs"
  type        = string
  sensitive   = true
}

variable "secret_to_spotify" {
  description = "JSON string of env vars for to-spotify"
  type        = string
  sensitive   = true
}

variable "secret_sort_playlists" {
  description = "JSON string of env vars for sort-playlists"
  type        = string
  sensitive   = true
}

locals {
  secrets = {
    from-youtube   = var.secret_from_youtube
    from-discogs   = var.secret_from_discogs
    to-spotify     = var.secret_to_spotify
    sort-playlists = var.secret_sort_playlists
  }
}

resource "aws_secretsmanager_secret" "function_secrets" {
  for_each = local.secrets
  name     = "homeplane/${each.key}"
}

resource "aws_secretsmanager_secret_version" "function_secrets" {
  for_each      = local.secrets
  secret_id     = aws_secretsmanager_secret.function_secrets[each.key].id
  secret_string = each.value
}
