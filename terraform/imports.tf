# One-time state moves and imports for resource renames.
# Can be removed after first successful apply.

# ECR repos: cloud_lambda → lambda (existing cloud repos renamed)
moved {
  from = aws_ecr_repository.cloud_lambda["to-www"]
  to   = aws_ecr_repository.lambda["to-www"]
}
moved {
  from = aws_ecr_repository.cloud_lambda["from-github"]
  to   = aws_ecr_repository.lambda["from-github"]
}

# Cloud Lambdas: cloud_lambda → cloud (resource renamed)
moved {
  from = aws_lambda_function.cloud_lambda["to-www"]
  to   = aws_lambda_function.cloud["to-www"]
}
moved {
  from = aws_lambda_function.cloud_lambda["from-github"]
  to   = aws_lambda_function.cloud["from-github"]
}

# Import ECR repos for fallback functions (created outside TF)
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

# Import fallback Lambda functions (created outside TF)
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
