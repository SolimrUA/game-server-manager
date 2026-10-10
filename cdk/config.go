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
// The accounts, referenced rather than created so replacing this stack can never delete them.
// COGNITO_USER_POOL_ID=eu-central-1_xxxxxxxxx
// COGNITO_DOMAIN_PREFIX=xxxxxxxxxxxxxx
//
// Optional.
// CONTROL_PANEL_STACK_NAME=game-server-controlpanel-cdk
// FLEET_STACK_NAME=game-server-fleet-cfn
// PUBLISHER_STACK_NAME=GameServerAssetPublisher
// MAX_SERVERS=10
// ID_TAG_NAME=game-server-finder
// ID_TAG_VALUE=game-server

type Config struct {
	AwsAccount string
	AwsRegion  string

	ControlPanelStackName string
	FleetStackName        string

	// The tag marking a server the control panel created. Backups and idle shutdown select on it, so a
	// server not carrying it is left alone by both.
	ManagedTag   string
	ManagedValue string

	PublisherStackName string
	// AmiParameter is the public SSM parameter holding the AMI a new server launches from.
	AmiParameter string
	// MaxServers caps how many servers can exist at once, whatever a caller asks for.
	MaxServers int

	FrontEndDomain string
	HostedZoneId   string
	HostedZoneName string

	UserPoolId          string
	CognitoDomainPrefix string
	CertificateArn      string

	IdTagName  string
	IdTagValue string

	ControlLambdaAssetDir string
	UpdateDnsAssetDir     string
	IdleShutdownAssetDir  string
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
		FleetStackName: stringEnv("FLEET_STACK_NAME", "game-server-fleet-cfn"),

		ManagedTag:   stringEnv("MANAGED_TAG", "managed-by"),
		ManagedValue: stringEnv("MANAGED_VALUE", "control-panel"),

		PublisherStackName: stringEnv("PUBLISHER_STACK_NAME", "GameServerAssetPublisher"),
		AmiParameter: stringEnv("AMI_PARAMETER",
			"/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id"),
		MaxServers: intEnv("MAX_SERVERS", 10),

		FrontEndDomain: stringEnv("FRONTEND_DOMAIN", ""),
		HostedZoneId:   stringEnv("HOSTED_ZONE_ID", ""),
		HostedZoneName: stringEnv("HOSTED_ZONE_NAME", ""),
		CertificateArn: stringEnv("CERTIFICATE_ARN", ""),

		UserPoolId:          requiredEnv("COGNITO_USER_POOL_ID"),
		CognitoDomainPrefix: requiredEnv("COGNITO_DOMAIN_PREFIX"),

		IdTagName:  stringEnv("ID_TAG_NAME", "game-server-finder"),
		IdTagValue: stringEnv("ID_TAG_VALUE", "game-server"),

		ControlLambdaAssetDir: "../build/lambda/control",
		UpdateDnsAssetDir:     "../build/lambda/update-dns",
		IdleShutdownAssetDir:  "../build/lambda/idle-shutdown",
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
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("invalid config line %q; expected KEY=value", line)
		}

		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" {
			return fmt.Errorf("invalid config line %q; key is empty", line)
		}

		if _, exists := os.LookupEnv(key); !exists {
			if err := os.Setenv(key, value); err != nil {
				return err
			}
		}
	}

	return scanner.Err()
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

func stringEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
