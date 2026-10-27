package main

import (
	"log"
	"os"

	"personal-game-server-manager/lambda/internal/control"
	loader "personal-game-server-manager/lambda/internal/env"
	"personal-game-server-manager/lambda/internal/provision"
)

// env reads this Lambda's configuration. The names are the ones the CDK sets.
type env struct {
	loader.Loader
}

// settings is everything read out of the environment, grouped by the package that needs it.
type settings struct {
	statusBucket     string
	accessTable      string
	userPoolID       string
	stateMachineArn  string
	backupLambdaName string

	control   control.Config
	provision provision.Config
}

func (e *env) read() settings {
	templates, err := provision.ParseTemplates(e.Optional("templates"))
	if err != nil {
		log.Fatal(err)
	}

	sizes := map[string]string{}
	var allowed []string
	for _, size := range instanceSizes {
		if instanceType := e.Require(size); instanceType != "" {
			sizes[size] = instanceType
			allowed = append(allowed, instanceType)
		}
	}

	return settings{
		statusBucket:     e.Require("statusBucket"),
		accessTable:      e.Optional("accessTable"),
		userPoolID:       e.Optional("userPoolId"),
		stateMachineArn:  e.Require("stepfunctionarn"),
		backupLambdaName: e.Require("backupLambdaName"),

		control: control.Config{
			TagName:             e.Require("idTagName"),
			TagValue:            e.Require("idTagValue"),
			StatusBucket:        e.Require("statusBucket"),
			BackupVaultName:     e.Optional("backupVaultName"),
			Region:              os.Getenv("AWS_REGION"),
			AccountID:           e.Require("accountId"),
			InstanceTypesBySize: sizes,
		},

		provision: provision.Config{
			Templates:            templates,
			SubnetID:             e.Require("subnetId"),
			InstanceProfileArn:   e.Require("instanceProfileArn"),
			AmiParameter:         e.Require("amiParameter"),
			StatusBucket:         e.Require("statusBucket"),
			ManagedTag:           e.Require("managedTag"),
			ManagedValue:         e.Require("managedValue"),
			BackupTag:            e.Require("backupTag"),
			BackupValue:          e.Require("backupValue"),
			IDTagName:            e.Require("idTagName"),
			IDTagValue:           e.Require("idTagValue"),
			HostedZoneID:         e.Optional("hostedZoneId"),
			HostedZoneName:       e.Optional("hostedZoneName"),
			MaxServers:           e.Int("maxServers", 10),
			AllowedInstanceTypes: allowed,
		},
	}
}
