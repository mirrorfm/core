# Static website hosting for mirror.fm
# S3 (private) + CloudFront (OAC) + ACM (us-east-1) + Route53

# --- Route53 ---

resource "aws_route53_zone" "mirror_fm" {
  name = "mirror.fm"
}

# --- ACM Certificate (must be in us-east-1 for CloudFront) ---

resource "aws_acm_certificate" "mirror_fm" {
  provider          = aws.us_east_1
  domain_name       = "mirror.fm"
  subject_alternative_names = ["*.mirror.fm"]
  validation_method = "DNS"

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.mirror_fm.domain_validation_options : dvo.domain_name => {
      name   = dvo.resource_record_name
      record = dvo.resource_record_value
      type   = dvo.resource_record_type
    }
  }

  allow_overwrite = true
  name            = each.value.name
  records         = [each.value.record]
  ttl             = 60
  type            = each.value.type
  zone_id         = aws_route53_zone.mirror_fm.zone_id
}

resource "aws_acm_certificate_validation" "mirror_fm" {
  provider                = aws.us_east_1
  certificate_arn         = aws_acm_certificate.mirror_fm.arn
  validation_record_fqdns = [for record in aws_route53_record.cert_validation : record.fqdn]
}

# --- S3 Bucket (private, no public access) ---

resource "aws_s3_bucket" "www" {
  bucket = "mirrorfm-www"
}

resource "aws_s3_bucket_public_access_block" "www" {
  bucket                  = aws_s3_bucket.www.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_policy" "www" {
  bucket = aws_s3_bucket.www.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid       = "AllowCloudFrontOAC"
        Effect    = "Allow"
        Principal = { Service = "cloudfront.amazonaws.com" }
        Action    = "s3:GetObject"
        Resource  = "${aws_s3_bucket.www.arn}/*"
        Condition = {
          StringEquals = {
            "AWS:SourceArn" = aws_cloudfront_distribution.www.arn
          }
        }
      }
    ]
  })
}

# --- CloudFront ---

resource "aws_cloudfront_origin_access_control" "www" {
  name                              = "mirrorfm-www"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "www" {
  origin {
    domain_name              = aws_s3_bucket.www.bucket_regional_domain_name
    origin_id                = "s3-www"
    origin_access_control_id = aws_cloudfront_origin_access_control.www.id
  }

  enabled             = true
  is_ipv6_enabled     = true
  default_root_object = "index.html"
  aliases             = ["mirror.fm", "www.mirror.fm"]
  price_class         = "PriceClass_100"

  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "s3-www"

    forwarded_values {
      query_string = false
      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 300
    max_ttl                = 86400
    compress               = true
  }

  # Long cache for Gatsby static assets (hashed filenames)
  ordered_cache_behavior {
    path_pattern     = "/static/*"
    allowed_methods  = ["GET", "HEAD"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "s3-www"

    forwarded_values {
      query_string = false
      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 31536000
    max_ttl                = 31536000
    compress               = true
  }

  # Client-side routing: serve index.html for unknown paths
  custom_error_response {
    error_code         = 403
    response_code      = 200
    response_page_path = "/index.html"
  }

  custom_error_response {
    error_code         = 404
    response_code      = 200
    response_page_path = "/index.html"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    acm_certificate_arn      = aws_acm_certificate_validation.mirror_fm.certificate_arn
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }

  depends_on = [aws_acm_certificate_validation.mirror_fm]
}

# --- Route53 Records ---

resource "aws_route53_record" "apex" {
  zone_id = aws_route53_zone.mirror_fm.zone_id
  name    = "mirror.fm"
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.www.domain_name
    zone_id                = aws_cloudfront_distribution.www.hosted_zone_id
    evaluate_target_health = false
  }
}

resource "aws_route53_record" "apex_aaaa" {
  zone_id = aws_route53_zone.mirror_fm.zone_id
  name    = "mirror.fm"
  type    = "AAAA"

  alias {
    name                   = aws_cloudfront_distribution.www.domain_name
    zone_id                = aws_cloudfront_distribution.www.hosted_zone_id
    evaluate_target_health = false
  }
}

resource "aws_route53_record" "www_redirect" {
  zone_id = aws_route53_zone.mirror_fm.zone_id
  name    = "www.mirror.fm"
  type    = "A"

  alias {
    name                   = aws_cloudfront_distribution.www.domain_name
    zone_id                = aws_cloudfront_distribution.www.hosted_zone_id
    evaluate_target_health = false
  }
}

# --- Outputs ---

output "cloudfront_distribution_id" {
  value = aws_cloudfront_distribution.www.id
}

output "cloudfront_domain_name" {
  value = aws_cloudfront_distribution.www.domain_name
}

output "s3_bucket_name" {
  value = aws_s3_bucket.www.id
}

output "route53_nameservers" {
  value       = aws_route53_zone.mirror_fm.name_servers
  description = "Set these as NS records at your domain registrar"
}
