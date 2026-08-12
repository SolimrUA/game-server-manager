package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const localConfigFile = ".env"

// Copy this block into CdkAssetPublisher/.env and replace values.
// Values already present in the process environment take precedence.
//
// AWS_ACCOUNT=123456789012
// AWS_REGION=eu-central-1
// ASSET_BUCKET_NAME=replace-with-your-assets-bucket
// CREATE_ASSET_BUCKET=true
// ASSET_KEY_PREFIX=personal-game-server-manager/v1
//
// Optional path overrides, relative to CdkAssetPublisher/.
// COMMON_SOURCE_TEMPLATE_PATH=../cfn/mcCommonInfra.yaml
// COMMON_OUTPUT_TEMPLATE_PATH=../build/mcCommonInfra.assets.yaml
// SERVER_SOURCE_TEMPLATE_PATH=../cfn/mcServerStack.yaml
// SERVER_OUTPUT_TEMPLATE_PATH=../build/mcServerStack.assets.yaml
// CONTROLPANEL_SOURCE_TEMPLATE_PATH=../cfn/mcControlPanel.yaml
// CONTROLPANEL_OUTPUT_TEMPLATE_PATH=../build/mcControlPanel.assets.yaml
// LOCAL_ASSET_BUILD_DIR=../build/assets
// LOCAL_LAMBDA_BUILD_DIR=../build/assets/Lambda
// LOCAL_FRONTEND_BUILD_DIR=../build/assets/FrontEnd
// LOCAL_BASH_BUILD_DIR=../build/assets/Bash

type Config struct {
	AwsAccount string
	AwsRegion  string

	AssetBucketName   string
	CreateAssetBucket bool
	AssetKeyPrefix    string

	CommonSourceTemplatePath       string
	CommonOutputTemplatePath       string
	ServerSourceTemplatePath       string
	ServerOutputTemplatePath       string
	ControlPanelSourceTemplatePath string
	ControlPanelOutputTemplatePath string

	LocalAssetBuildDir    string
	LocalLambdaBuildDir   string
	LocalFrontendBuildDir string
	LocalBashBuildDir     string
}

func LoadConfig() (Config, error) {
	if err := loadEnvFile(localConfigFile); err != nil {
		return Config{}, err
	}

	config := Config{
		AwsAccount:        requiredEnv("AWS_ACCOUNT"),
		AwsRegion:         requiredEnv("AWS_REGION"),
		AssetBucketName:   requiredEnv("ASSET_BUCKET_NAME"),
		CreateAssetBucket: boolEnv("CREATE_ASSET_BUCKET", true),
		AssetKeyPrefix:    stringEnv("ASSET_KEY_PREFIX", "personal-game-server-manager/v1"),

		CommonSourceTemplatePath:       stringEnv("COMMON_SOURCE_TEMPLATE_PATH", "../cfn/mcCommonInfra.yaml"),
		CommonOutputTemplatePath:       stringEnv("COMMON_OUTPUT_TEMPLATE_PATH", "../build/mcCommonInfra.assets.yaml"),
		ServerSourceTemplatePath:       stringEnv("SERVER_SOURCE_TEMPLATE_PATH", "../cfn/mcServerStack.yaml"),
		ServerOutputTemplatePath:       stringEnv("SERVER_OUTPUT_TEMPLATE_PATH", "../build/mcServerStack.assets.yaml"),
		ControlPanelSourceTemplatePath: stringEnv("CONTROLPANEL_SOURCE_TEMPLATE_PATH", "../cfn/mcControlPanel.yaml"),
		ControlPanelOutputTemplatePath: stringEnv("CONTROLPANEL_OUTPUT_TEMPLATE_PATH", "../build/mcControlPanel.assets.yaml"),

		LocalAssetBuildDir: stringEnv("LOCAL_ASSET_BUILD_DIR", "../build/assets"),
	}

	config.LocalLambdaBuildDir = stringEnv("LOCAL_LAMBDA_BUILD_DIR", config.LocalAssetBuildDir+"/Lambda")
	config.LocalFrontendBuildDir = stringEnv("LOCAL_FRONTEND_BUILD_DIR", config.LocalAssetBuildDir+"/FrontEnd")
	config.LocalBashBuildDir = stringEnv("LOCAL_BASH_BUILD_DIR", config.LocalAssetBuildDir+"/Bash")

	if err := validateConfig(config); err != nil {
		return Config{}, err
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

func validateConfig(config Config) error {
	for label, path := range map[string]string{
		"COMMON_OUTPUT_TEMPLATE_PATH":       config.CommonOutputTemplatePath,
		"SERVER_OUTPUT_TEMPLATE_PATH":       config.ServerOutputTemplatePath,
		"CONTROLPANEL_OUTPUT_TEMPLATE_PATH": config.ControlPanelOutputTemplatePath,
		"LOCAL_ASSET_BUILD_DIR":             config.LocalAssetBuildDir,
		"LOCAL_LAMBDA_BUILD_DIR":            config.LocalLambdaBuildDir,
		"LOCAL_FRONTEND_BUILD_DIR":          config.LocalFrontendBuildDir,
		"LOCAL_BASH_BUILD_DIR":              config.LocalBashBuildDir,
	} {
		if err := requireRepoBuildPath(label, path); err != nil {
			return err
		}
	}

	return nil
}

func requireRepoBuildPath(label, path string) error {
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		return err
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	relativePath, err := filepath.Rel(repoRoot, absolutePath)
	if err != nil {
		return err
	}

	if relativePath == "." || strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)) || filepath.IsAbs(relativePath) {
		return fmt.Errorf("%s must stay inside the repository build directory, got %q", label, path)
	}

	cleanRelativePath := filepath.Clean(relativePath)
	if cleanRelativePath != "build" && !strings.HasPrefix(cleanRelativePath, "build"+string(os.PathSeparator)) {
		return fmt.Errorf("%s must stay inside the repository build directory, got %q", label, path)
	}

	return nil
}

func requiredEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		panic(fmt.Sprintf("required config value %s is empty", key))
	}
	return value
}

func stringEnv(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func boolEnv(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}

	switch value {
	case "1", "true", "yes", "y":
		return true
	case "0", "false", "no", "n":
		return false
	default:
		panic(fmt.Sprintf("invalid boolean value for %s: %q", key, value))
	}
}
