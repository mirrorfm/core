# DynamoDB table for artist submission interest signals.
# Captures which artists want to submit to which channels,
# used to build demand data before channel onboarding.

resource "aws_dynamodb_table" "interests" {
  name         = "mirrorfm_interests"
  billing_mode = "PAY_PER_REQUEST"

  hash_key  = "email"
  range_key = "track_url"

  attribute {
    name = "email"
    type = "S"
  }

  attribute {
    name = "track_url"
    type = "S"
  }
}

resource "aws_dynamodb_table" "users" {
  name         = "mirrorfm_users"
  billing_mode = "PAY_PER_REQUEST"

  hash_key = "user_id"

  attribute {
    name = "user_id"
    type = "S"
  }
}

resource "aws_dynamodb_table" "takedowns" {
  name         = "mirrorfm_takedowns"
  billing_mode = "PAY_PER_REQUEST"

  hash_key = "id"

  attribute {
    name = "id"
    type = "S"
  }
}

# submissions and credit_txns are in MySQL (see schema.sql)
