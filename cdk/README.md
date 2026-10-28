# CDK stacks

Two stacks, deployed together with `cdk deploy --all`:

- **Fleet** (`game-server-fleet-cfn`) - what every game server shares: the role they run under, a
  security group per game, one backup plan, and the schedule that stops idle servers.
- **Control Panel** - the web site, the API behind it, and the Lambda that acts on the game servers.

Copy the block at the top of [`config.go`](config.go) into `.env`, which git ignores.

## Deploying

```bash
cdk deploy --all
```

Three stacks: the assets bucket the game scripts are published to, the fleet every server shares, and
the control panel with its web site. A script's digest is computed from the file being published, and
`js/config.js` is filled in from the control panel's own constructs, so a deploy needs nothing read
back from a previous one.

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

An external identity provider is referenced by name, in `OIDC_PROVIDER_NAME`, and offered as a
sign-in option beside the pool's own. It lives on the pool, like the accounts, and must exist before a
deploy names it; empty offers the pool's own sign-in alone.

The TLS certificate is referenced by ARN: CloudFront requires it in us-east-1.

The assets bucket is referenced by name, and created here only when `CREATE_ASSET_BUCKET` is set. Its
policy grants public `s3:GetObject` on the published prefix, because an instance fetches its install
script before it has credentials of its own.
