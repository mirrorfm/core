variable "lambda_function_names" {
  description = "Map of function key to existing Lambda function name in AWS. Verify these match your actual Lambda names."
  type        = map(string)
  default = {
    from-youtube   = "mirror-fm_from-youtube"
    from-discogs   = "mirror-fm_from-discogs"
    to-spotify     = "mirror-fm_to-spotify"
    manage-playlists = "mirror-fm_sort-playlists"
  }
}
