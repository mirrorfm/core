# One-time imports and moves for existing resources now managed by Terraform.
# These blocks can be removed after the first successful apply.

# Remove old resource names from state (renamed, not deleted)
removed {
  from = aws_ecr_repository.cloud_lambda["to-www"]
  lifecycle { destroy = false }
}
removed {
  from = aws_ecr_repository.cloud_lambda["from-github"]
  lifecycle { destroy = false }
}
removed {
  from = aws_lambda_function.cloud_lambda["to-www"]
  lifecycle { destroy = false }
}
removed {
  from = aws_lambda_function.cloud_lambda["from-github"]
  lifecycle { destroy = false }
}
removed {
  from = aws_lambda_function.cloud_lambda["to-www"]
  lifecycle { destroy = false }
}

# ECR repositories
import {
  to = aws_ecr_repository.lambda["to-www"]
  id = "to-www"
}
import {
  to = aws_ecr_repository.lambda["from-github"]
  id = "from-github"
}
import {
  to = aws_ecr_repository.lambda["from-youtube"]
  id = "from-youtube"
}
import {
  to = aws_ecr_repository.lambda["from-discogs"]
  id = "from-discogs"
}
import {
  to = aws_ecr_repository.lambda["to-spotify"]
  id = "to-spotify"
}
import {
  to = aws_ecr_repository.lambda["manage-playlists"]
  id = "manage-playlists"
}

# Cloud Lambda functions (renamed from cloud_lambda to cloud)
import {
  to = aws_lambda_function.cloud["to-www"]
  id = "mirror-fm_to-www"
}
import {
  to = aws_lambda_function.cloud["from-github"]
  id = "mirror-fm_from-github"
}

# Fallback Lambda functions
import {
  to = aws_lambda_function.fallback["from-youtube"]
  id = "mirror-fm_from-youtube"
}
import {
  to = aws_lambda_function.fallback["from-discogs"]
  id = "mirror-fm_from-discogs"
}
import {
  to = aws_lambda_function.fallback["to-spotify"]
  id = "mirror-fm_to-spotify"
}
import {
  to = aws_lambda_function.fallback["manage-playlists"]
  id = "mirror-fm_manage-playlists"
}
