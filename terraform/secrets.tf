# AWS Secrets Manager secrets for k3s workers.
# External Secrets Operator syncs these into k8s Secrets.
#
# Values come from TF variables (set via TF_VAR_* env vars in GHA,
# sourced from GitHub Actions secrets). Never hardcoded in code.

variable "secret_from_youtube" {
  description = "JSON string of env vars for from-youtube"
  type        = string
  sensitive   = true
  default     = ""
}

variable "secret_from_discogs" {
  description = "JSON string of env vars for from-discogs"
  type        = string
  sensitive   = true
  default     = ""
}

variable "secret_to_spotify" {
  description = "JSON string of env vars for to-spotify"
  type        = string
  sensitive   = true
  default     = ""
}

variable "secret_manage_playlists" {
  description = "JSON string of env vars for manage-playlists"
  type        = string
  sensitive   = true
  default     = ""
}

variable "manage_secret_values" {
  description = "Whether to manage secret values (set to true in GHA, false for local plans)"
  type        = bool
  default     = false
}

locals {
  secret_names = toset(["from-youtube", "from-discogs", "to-spotify", "manage-playlists"])
  secrets = {
    from-youtube   = var.secret_from_youtube
    from-discogs   = var.secret_from_discogs
    to-spotify     = var.secret_to_spotify
    manage-playlists = var.secret_manage_playlists
  }
}

resource "aws_secretsmanager_secret" "function_secrets" {
  for_each = local.secret_names
  name     = "homeplane/${each.key}"
}

resource "aws_secretsmanager_secret_version" "function_secrets" {
  for_each      = var.manage_secret_values ? local.secret_names : toset([])
  secret_id     = aws_secretsmanager_secret.function_secrets[each.key].id
  secret_string = local.secrets[each.key]
}
