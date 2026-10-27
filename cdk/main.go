package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/jsii-runtime-go"
)

func main() {
	config, err := LoadConfig()
	must(err)
	must(buildLambdas(config))

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

// buildLambdas compiles every handler this app deploys. They share one Go module, and with it the
// packages that describe a server, read its status and run commands on it.
func buildLambdas(config Config) error {
	for _, lambda := range []struct {
		packagePath string
		assetDir    string
	}{
		{"./control", config.ControlLambdaAssetDir},
		{"./updatedns", config.UpdateDnsAssetDir},
		{"./idleshutdown", config.IdleShutdownAssetDir},
		{"./backupservers", config.BackupLambdaAssetDir},
	} {
		if err := buildGoLambda("../Lambda/go", lambda.packagePath, lambda.assetDir); err != nil {
			return err
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

	//-s -w leave out the symbol table and DWARF data, which nothing reads in Lambda and which are a
	//large part of the binary's size. -buildvcs=false leaves out the commit the build was made from,
	//which would otherwise change every binary on every commit and redeploy all four for no change.
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w",
		"-o", absolute, packagePath)
	build.Dir = moduleDir
	//Lambda runs linux/amd64 regardless of what this is built on, and CGO would need a matching libc.
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building %s: %w\n%s", packagePath, err, output)
	}
	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
