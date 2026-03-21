terraform {
  required_version = ">= 1.0"

  backend "s3" {
    bucket         = "homeplane-terraform-state-eu-west-1"
    key            = "mirrorfm-core/terraform.tfstate"
    region         = "eu-west-1"
    dynamodb_table = "homeplane-terraform-lock"
  }

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}

provider "aws" {
  alias  = "us_east_1"
  region = "us-east-1"
}
