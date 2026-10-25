package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	config, err := LoadConfig()
	must(err)
	must(prepareLambdaAssets(&config))
	must(buildGoLambda("../Lambda/go", "./backupservers", config.BackupLambdaAssetDir))

	app := awscdk.NewApp(nil)
	env := &awscdk.Environment{
		Account: jsii.String(config.AwsAccount),
		Region:  jsii.String(config.AwsRegion),
	}
	fleet := NewFleetStack(app, config.FleetStackName, &FleetStackProps{
		StackProps: awscdk.StackProps{Env: env},
		Config:     config,
	})

	publisherOutputs, err := lookupStackOutputs(config.PublisherStackName, config.AwsRegion)
	must(err)
	templates, err := resolveTemplates(publisherOutputs, fleet.SecurityGroupIds)
	must(err)

	NewControlPanelStack(app, config.ControlPanelStackName, &ControlPanelStackProps{
		StackProps: awscdk.StackProps{Env: env},
		Config:     config,
		Fleet:      fleet,
		Templates:  templates,
	})
	app.Synth(nil)
}

// prepareLambdaAssets stages each handler in its own directory as lambda_function.py, which is the
// module name the handler setting expects. The files are named per version in the repository, and a
// Python module name can't contain the dots and dashes those names use.
func prepareLambdaAssets(config *Config) error {
	for _, asset := range []struct {
		source string
		target *string
	}{
		{"../Lambda/gaming_server_start_stop-v1_0.py", &config.ControlLambdaAssetDir},
		{"../Lambda/update-dns-v1_0.py", &config.UpdateDnsAssetDir},
		{"../Lambda/idle_shutdown.py", &config.IdleShutdownAssetDir},
	} {
		if err := os.MkdirAll(*asset.target, 0o755); err != nil {
			return err
		}
		if err := copyFile(asset.source, filepath.Join(*asset.target, "lambda_function.py")); err != nil {
			return fmt.Errorf("staging %s: %w", asset.source, err)
		}
	}
	return nil
}

// buildGoLambda compiles a handler into its own asset directory as "bootstrap", the executable name
// the provided-al2023 runtime looks for.
func buildGoLambda(moduleDir, packagePath, assetDir string) error {
	absolute, err := filepath.Abs(filepath.Join(assetDir, "bootstrap"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		return err
	}

	build := exec.Command("go", "build", "-trimpath", "-o", absolute, packagePath)
	build.Dir = moduleDir
	//Lambda runs linux/amd64 regardless of what this is built on, and CGO would need a matching libc.
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building %s: %w\n%s", packagePath, err, output)
	}
	return nil
}

func copyFile(source, destination string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
