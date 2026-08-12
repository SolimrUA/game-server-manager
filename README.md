## Personal Gamer Server Manager

Hosting your own personal gaming server is increasingly common given all the benefits and flexibility it provides, however, doing so in a secure, flexible, and cost-effective manner is not simple. To achieve low cost many people host a server on their home computer, requiring them to open up their home network firewall ports in the process and exposing their computer to the wider internet and the associated security risks. Playing a game while also hosting the server for it can be a heavy task for any computer, so often players see the best performance while running a server and client on separate machines. To overcome these challenges players often look to dedicated game-server companies which provide varying degrees of control over the underlying server such as requiring fixed server sizes, giving limited access to mods, or simply charging you a flat monthly fee no matter how much you utilize the server. 

In the below blog post we will show you how to achieve both a low cost and high security solution while also providing the added benefit of flexibility to resize the server from a single core and 0.5 GiB of memory all the way to the biggest servers AWS has to offer and back down again.  

More details and instructions on this solution can be found on the AWS Gametech blog here: https://aws.amazon.com/blogs/gametech//hosting-your-own-dedicated-valheim-server-in-the-cloud/

## Deploying

The solution is split into three CloudFormation templates under [`cfn/`](cfn/), deployed in this order:

1. [`cfn/mcCommonInfra.yaml`](cfn/mcCommonInfra.yaml) - shared VPC/networking. Deploy once per account+region.
2. [`cfn/mcControlPanel.yaml`](cfn/mcControlPanel.yaml) - the shared Cognito login, control API, and web control site. Deploy once.
3. [`cfn/mcServerStack.yaml`](cfn/mcServerStack.yaml) - one game server. Deploy again for each additional server you want to run (e.g. once for Valheim, again for Vintage Story) - each just needs the "game cartridge" install script URL for that game (see [`Bash/`](Bash/)).

`IdTagName`/`IdTagValue` must be entered identically on the Control Panel stack and every Server stack - the Control Panel finds servers to manage purely by that EC2 tag at runtime, with no other link between the stacks. `HostedZoneId` must likewise match between the Control Panel stack and any Server stack using a custom `Domain`.

If you have the older single-template version of this solution deployed, note that splitting into three stacks is a breaking change - CloudFormation can't migrate resources out of a live stack into new ones. Back up anything you care about, delete the old stack, then deploy the three new stacks above.

If you're publishing your own copy of the assets (Lambda code, frontend files, game cartridges) to S3 instead of pulling them from GitHub at deploy time, see [`CdkAssetPublisher/README.md`](CdkAssetPublisher/README.md).

## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the LICENSE file.

