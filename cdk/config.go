package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const localConfigFile = ".env"

// Copy this block into cdk/.env and replace values.
// Values already present in the process environment take precedence.
//
// AWS_ACCOUNT=123456789012
// AWS_REGION=eu-central-1
//
// The custom domain, and the certificate and hosted zone that serve it. Leave all four blank while
// another stack still claims the domain: CloudFront refuses an alternate domain name already in use,
// and the site is fully usable on the distribution's own domain until the handover.
// FRONTEND_DOMAIN=servers.example.com
// HOSTED_ZONE_ID=Z0000000000000000000
// HOSTED_ZONE_NAME=example.com
// CERTIFICATE_ARN=arn:aws:acm:us-east-1:123456789012:certificate/...
//
// The bucket the game scripts are published to. An instance fetches its install script from here
// over HTTPS before it has credentials, so the published prefix is readable by anyone. Set
// CREATE_ASSET_BUCKET=false for a bucket that already exists and should only be published into.
// ASSET_BUCKET_NAME=your-unique-bucket-name
// CREATE_ASSET_BUCKET=true
// ASSET_KEY_PREFIX=personal-game-server-manager/v1
//
// The accounts, referenced rather than created so replacing this stack can never delete them.
// COGNITO_USER_POOL_ID=eu-central-1_xxxxxxxxx
// COGNITO_DOMAIN_PREFIX=xxxxxxxxxxxxxx
//
// The prefix list allowed to reach port 22 on every server, for EC2 Instance Connect. Left blank,
// no server accepts SSH from anywhere.
// SSH_PREFIX_LIST=pl-00000000000000000
//
// Optional.
// CONTROL_PANEL_STACK_NAME=game-server-controlpanel-cdk
// FLEET_STACK_NAME=game-server-fleet-cfn
// ASSETS_STACK_NAME=game-server-assets-cfn
// MAX_SERVERS=10
// SUPERSEDED_BACKUP_DAYS=30
// ID_TAG_NAME=game-server-finder
// ID_TAG_VALUE=game-server

// dataDeviceName is where a server's data volume attaches. The install scripts find the volume by
// looking for the one disk that isn't the root, so this only has to be free.
const dataDeviceName = "/dev/sdf"

type Config struct {
	AwsAccount string
	AwsRegion  string

	ControlPanelStackName string
	FleetStackName        string
	AssetsStackName       string

	// The tag marking a server the control panel created. Backups and idle shutdown select on it, so a
	// server not carrying it is left alone by both.
	ManagedTag   string
	ManagedValue string
	// The tag marking what the backup Lambda protects. A server's data volume carries it and nothing
	// else does, so the Lambda finds its volumes by it.
	BackupTag   string
	BackupValue string
	// How many backups of each server to keep.
	// How long a backup lives once a newer one supersedes it. Only the newest is kept indefinitely, so
	// a deleted server keeps one world and lets the rest run out.
	SupersededBackupDays int

	// AssetBucketName is the bucket the game scripts are published to and fetched from over HTTPS.
	// CreateAssetBucket is false when it already exists and this app should only publish into it.
	AssetBucketName   string
	CreateAssetBucket bool
	AssetKeyPrefix    string
	// AmiParameter is the public SSM parameter holding the AMI a new server launches from.
	AmiParameter string
	// MaxServers caps how many servers can exist at once, whatever a caller asks for.
	MaxServers int

	FrontEndDomain string
	HostedZoneId   string
	HostedZoneName string

	// SshPrefixList is the managed prefix list allowed to reach port 22 on every server. Empty leaves
	// SSH closed.
	SshPrefixList string

	UserPoolId          string
	CognitoDomainPrefix string
	CertificateArn      string

	// AuthentikProviderName must match an OIDC provider that already exists on the pool: nothing here
	// creates one, and naming an absent provider fails the deploy. Empty offers the pool's own
	// sign-in alone.
	AuthentikProviderName string

	IdTagName  string
	IdTagValue string

	ControlLambdaAssetDir string
	UpdateDnsAssetDir     string
	IdleShutdownAssetDir  string
	BackupLambdaAssetDir  string
}

func LoadConfig() (Config, error) {
	if err := loadEnvFile(localConfigFile); err != nil {
		return Config{}, err
	}

	config := Config{
		AwsAccount: requiredEnv("AWS_ACCOUNT"),
		AwsRegion:  requiredEnv("AWS_REGION"),

		// Deliberately not the name of the CloudFormation stack this replaces: the two stand side by
		// side until the old one is deleted.
		ControlPanelStackName: stringEnv("CONTROL_PANEL_STACK_NAME", "game-server-controlpanel-cdk"),
		//Must match the game-server-*-cfn pattern the deploy role is scoped to.
		FleetStackName:  stringEnv("FLEET_STACK_NAME", "game-server-fleet-cfn"),
		AssetsStackName: stringEnv("ASSETS_STACK_NAME", "game-server-assets-cfn"),

		ManagedTag:           stringEnv("MANAGED_TAG", "managed-by"),
		ManagedValue:         stringEnv("MANAGED_VALUE", "control-panel"),
		BackupTag:            stringEnv("BACKUP_TAG", "backup"),
		BackupValue:          stringEnv("BACKUP_VALUE", "daily"),
		SupersededBackupDays: intEnv("SUPERSEDED_BACKUP_DAYS", 30),

		AssetBucketName:   requiredEnv("ASSET_BUCKET_NAME"),
		CreateAssetBucket: boolEnv("CREATE_ASSET_BUCKET", true),
		AssetKeyPrefix:    stringEnv("ASSET_KEY_PREFIX", "personal-game-server-manager/v1"),
		AmiParameter: stringEnv("AMI_PARAMETER",
			"/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"),
		MaxServers: intEnv("MAX_SERVERS", 10),

		SshPrefixList: stringEnv("SSH_PREFIX_LIST", ""),

		FrontEndDomain: stringEnv("FRONTEND_DOMAIN", ""),
		HostedZoneId:   stringEnv("HOSTED_ZONE_ID", ""),
		HostedZoneName: stringEnv("HOSTED_ZONE_NAME", ""),
		CertificateArn: stringEnv("CERTIFICATE_ARN", ""),

		UserPoolId:          requiredEnv("COGNITO_USER_POOL_ID"),
		CognitoDomainPrefix: requiredEnv("COGNITO_DOMAIN_PREFIX"),

		AuthentikProviderName: stringEnv("OIDC_PROVIDER_NAME", ""),

		IdTagName:  stringEnv("ID_TAG_NAME", "game-server-finder"),
		IdTagValue: stringEnv("ID_TAG_VALUE", "game-server"),

		ControlLambdaAssetDir: "../build/lambda/control",
		UpdateDnsAssetDir:     "../build/lambda/update-dns",
		IdleShutdownAssetDir:  "../build/lambda/idle-shutdown",
		BackupLambdaAssetDir:  "../build/lambda/backup-servers",
	}

	if config.FrontEndDomain != "" {
		for _, companion := range []struct {
			key   string
			value string
		}{
			{"HOSTED_ZONE_ID", config.HostedZoneId},
			{"HOSTED_ZONE_NAME", config.HostedZoneName},
			{"CERTIFICATE_ARN", config.CertificateArn},
		} {
			if companion.value == "" {
				return Config{}, fmt.Errorf("FRONTEND_DOMAIN is set, so %s is required too", companion.key)
			}
		}
	}

	return config, nil
}

func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, err := envAssignment(scanner.Text())
		if err != nil {
			return err
		}
		if key == "" {
			continue
		}
		if _, alreadySet := os.LookupEnv(key); alreadySet {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	return scanner.Err()
}

// envAssignment reads one KEY=value line of the config file. A blank line or a comment is not an
// assignment, and comes back with an empty key.
func envAssignment(text string) (string, string, error) {
	line := strings.TrimSpace(text)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", nil
	}

	key, value, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", fmt.Errorf("invalid config line %q; expected KEY=value", line)
	}

	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("invalid config line %q; key is empty", line)
	}
	return key, strings.Trim(strings.TrimSpace(value), `"'`), nil
}

func requiredEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		panic(fmt.Sprintf("required config value %s is empty", key))
	}
	return value
}

func intEnv(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		panic(fmt.Sprintf("invalid integer value for %s: %q", key, value))
	}
	return parsed
}

func boolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

func stringEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
