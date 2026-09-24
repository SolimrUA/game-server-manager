# Control panel web site

Static site for the game server control panel, published to S3/CloudFront by the Control Panel stack.
`js/config.js`'s `REPLACE-WITH-*` placeholders are filled in at deploy time with that stack's Cognito,
API and CloudFront values.
