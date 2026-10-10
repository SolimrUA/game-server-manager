# CDK stacks

The control panel: the web site, the API behind it, and the Lambda that acts on the game servers.
`cdk deploy` creates `game-server-controlpanel-cdk`.

Copy the block at the top of [`config.go`](config.go) into `.env`, which git ignores.

## Deploying

```bash
cd CdkAssetPublisher && cdk deploy     # Lambda code and game scripts
cd ../Cdk && cdk deploy                # this stack
cd ../CdkAssetPublisher && cdk deploy  # the web site, from this stack's outputs
```

`CONTROL_PANEL_STACK_NAME` tells the publisher which stack's outputs fill `js/config.js`.

## The custom domain is optional

Set `FRONTEND_DOMAIN` with its hosted zone and certificate, and CloudFront serves that domain with an
A-record pointing at it. Leave all four blank and the site answers on the distribution's own CloudFront
domain. Either way the app client's callback URLs follow whichever host serves the site, which is the
only host Cognito will accept them for.

CloudFront refuses an alternate domain name that another distribution already serves.

## Referenced, not declared

The Cognito user pool, its hosted-UI domain and its `admins` group are referenced by id. They hold the
accounts, and a pool allows only one domain and one group of a given name. The app client belongs to
this stack; a pool takes as many of those as you like.

The TLS certificate is referenced by ARN: CloudFront requires it in us-east-1.

The web bucket is created here and filled by `CdkAssetPublisher`, which reads this stack's outputs.
