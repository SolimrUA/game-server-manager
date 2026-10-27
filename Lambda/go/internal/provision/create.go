package provision

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// CreateRequest is a new server as the caller asked for it. Everything here is checked before any
// of it is used.
type CreateRequest struct {
	Game         string `json:"game"`
	ServerID     string `json:"serverId"`
	InstanceType string `json:"instanceType"`
	GameVersion  string `json:"gameVersion"`
	VolumeSize   string `json:"volumeSize"`
	// SnapshotID names the backup to start the world from. Empty creates an empty world.
	SnapshotID string `json:"snapshotId"`
}

// createRequest is a CreateRequest once checked: every field is known good and typed.
type createRequest struct {
	Game         string
	ServerID     string
	InstanceType string
	GameVersion  string
	VolumeSize   int32
	RestoredFrom *Backup
}

// Create launches a server, which installs its game on first boot.
func (p *Provisioner) Create(ctx context.Context, asked CreateRequest) (string, error) {
	req, template, err := p.check(ctx, asked)
	if err != nil {
		return "", err
	}

	ami, err := p.ami(ctx)
	if err != nil {
		return "", err
	}

	if err := p.status.MarkInstalling(ctx, req.ServerID); err != nil {
		// The server still installs; only the control panel's reading of how it went is affected.
		log.Printf("couldn't mark %s as installing: %v", req.ServerID, err)
	}

	urls, err := p.installURLs(ctx, req.ServerID)
	if err != nil {
		return "", err
	}

	instanceTags, dataTags, rootTags := p.tagsFor(req, template)

	source := ""
	if req.RestoredFrom != nil {
		source = req.RestoredFrom.ServerID
	}
	passwordSource, err := p.ensurePassword(ctx, req.ServerID, source)
	if err != nil {
		return "", err
	}

	out, err := p.ec2.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:             aws.String(ami),
		InstanceType:        ec2types.InstanceType(req.InstanceType),
		MinCount:            aws.Int32(1),
		MaxCount:            aws.Int32(1),
		SubnetId:            aws.String(p.config.SubnetID),
		SecurityGroupIds:    []string{template.SecurityGroupID},
		IamInstanceProfile:  &ec2types.IamInstanceProfileSpecification{Arn: aws.String(p.config.InstanceProfileArn)},
		UserData:            aws.String(base64.StdEncoding.EncodeToString([]byte(userData(req, template, urls, p.config.StatusBucket)))),
		BlockDeviceMappings: p.blockDevices(req),
		// Only the instance is tagged here: tagging volumes at launch would tag the root disk too,
		// and the backup Lambda selects data volumes on that tag.
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeInstance,
			Tags:         instanceTags,
		}},
	})
	if err != nil {
		return "", fmt.Errorf("launching a server for %s: %w", req.ServerID, err)
	}
	if len(out.Instances) == 0 {
		return "", fmt.Errorf("launching a server for %s: no instance returned", req.ServerID)
	}
	instanceID := aws.ToString(out.Instances[0].InstanceId)

	// Started before the slow work below: a new instance has no address for a moment, so naming it is
	// left to the state machine that waits for one.
	if domain := p.domainFor(req.ServerID); domain != "" {
		if err := p.launcher.AfterLaunch(ctx, instanceID, domain, p.config.HostedZoneID); err != nil {
			log.Printf("couldn't start the after-launch steps for %s: %v", instanceID, err)
		}
	}

	dataVolumeID, err := p.tagVolumes(ctx, instanceID, dataTags, rootTags)
	if err != nil {
		log.Printf("couldn't name the volumes of %s: %v", instanceID, err)
	}

	log.Printf("created server %s (%s) running %s as %s", req.ServerID, instanceID, req.Game, nameOf(instanceTags))
	return created(req, template, instanceID, dataVolumeID, passwordSource), nil
}

// created is what the caller is told, including what didn't go to plan but didn't stop the server.
func created(req createRequest, template Template, instanceID, dataVolumeID string,
	passwordSource PasswordSource) string {

	var b strings.Builder
	fmt.Fprintf(&b, "Created %s (%s) - it installs %s on first boot, which takes a few minutes",
		req.ServerID, instanceID, template.DisplayName)

	if dataVolumeID == "" {
		b.WriteString(". Its data volume couldn't be tagged, so it won't be backed up")
	}
	if req.RestoredFrom != nil && passwordSource == PasswordNew {
		fmt.Fprintf(&b, ". Its join password couldn't be taken from the backup's own server, so players "+
			"keep the one that world already had rather than the one stored for %s", req.ServerID)
	}
	return b.String()
}

// check turns a request into one that is safe to act on, reporting the first thing wrong with it in
// words meant for whoever asked.
func (p *Provisioner) check(ctx context.Context, asked CreateRequest) (createRequest, Template, error) {
	var none createRequest

	game := strings.TrimSpace(asked.Game)
	template, known := p.config.Templates[game]
	if !known {
		return none, Template{}, usererr.Newf("Unknown game %q - pick one of: %s",
			game, strings.Join(p.config.Templates.Games(), ", "))
	}

	serverID := strings.ToLower(strings.TrimSpace(asked.ServerID))
	if err := p.checkName(ctx, serverID); err != nil {
		return none, Template{}, err
	}

	instanceType, err := p.checkInstanceType(asked.InstanceType, template)
	if err != nil {
		return none, Template{}, err
	}

	gameVersion := strings.TrimSpace(asked.GameVersion)
	if template.RequiresGameVersion && gameVersion == "" {
		return none, Template{}, usererr.Newf("%s needs an exact game version", template.DisplayName)
	}

	volumeSize, err := checkVolumeSize(asked.VolumeSize, template)
	if err != nil {
		return none, Template{}, err
	}

	restoredFrom, err := p.checkRestore(ctx, asked.SnapshotID, game, template)
	if err != nil {
		return none, Template{}, err
	}

	return createRequest{
		Game:         game,
		ServerID:     serverID,
		InstanceType: instanceType,
		GameVersion:  gameVersion,
		VolumeSize:   volumeSize,
		RestoredFrom: restoredFrom,
	}, template, nil
}

// checkName accepts a server id that is well formed, unused, and within the fleet's size limit.
func (p *Provisioner) checkName(ctx context.Context, serverID string) error {
	if !serverIDPattern.MatchString(serverID) {
		return usererr.New("A server id must be 3-32 characters of lowercase letters, numbers and hyphens")
	}

	existing, err := p.existing(ctx)
	if err != nil {
		return err
	}
	if len(existing) >= p.config.MaxServers {
		return usererr.Newf("There are already %d servers, which is the limit", p.config.MaxServers)
	}
	for _, s := range existing {
		if s.ServerID == serverID {
			return usererr.Newf("A server called %q already exists", serverID)
		}
	}
	return nil
}

// checkInstanceType falls back to the game's default, and accepts only the sizes the control panel
// offers - which are also the ones its IAM policy conditions RunInstances on.
func (p *Provisioner) checkInstanceType(asked string, template Template) (string, error) {
	instanceType := strings.TrimSpace(asked)
	if instanceType == "" {
		instanceType = template.DefaultInstanceType
	}
	if !slices.Contains(p.config.AllowedInstanceTypes, instanceType) {
		return "", usererr.Newf("Instance type must be one of: %s",
			strings.Join(p.config.AllowedInstanceTypes, ", "))
	}
	return instanceType, nil
}

// checkVolumeSize falls back to the game's default when none was asked for.
func checkVolumeSize(asked string, template Template) (int32, error) {
	size := int32(template.DefaultVolumeSizeGib)
	if strings.TrimSpace(asked) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(asked))
		if err != nil {
			return 0, usererr.New("Volume size must be a whole number of GiB")
		}
		size = int32(parsed)
	}
	if size < minVolumeSizeGib {
		return 0, usererr.Newf("Volume size must be at least %d GiB", minVolumeSizeGib)
	}
	return size, nil
}

// checkRestore reads the backup a world is to be started from, and returns nil for a fresh world.
func (p *Provisioner) checkRestore(ctx context.Context, snapshotID, game string, template Template) (*Backup, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return nil, nil
	}

	backup, err := p.backup(ctx, strings.TrimSpace(snapshotID))
	if err != nil {
		return nil, err
	}
	if backup.Game != "" && backup.Game != game {
		return nil, usererr.Newf("That backup holds a %s world, which a %s server can't run",
			backup.Game, template.DisplayName)
	}
	return backup, nil
}

// blockDevices describes the data volume. It is made from the backup as the instance launches, so it
// is there when the installer looks for it.
func (p *Provisioner) blockDevices(req createRequest) []ec2types.BlockDeviceMapping {
	data := &ec2types.EbsBlockDevice{
		VolumeType: ec2types.VolumeTypeGp3,
		// The world outlives its server, and is kept as that server's backups.
		DeleteOnTermination: aws.Bool(false),
	}
	if req.RestoredFrom != nil {
		data.SnapshotId = aws.String(req.RestoredFrom.SnapshotID)
	} else {
		data.VolumeSize = aws.Int32(req.VolumeSize)
	}

	return []ec2types.BlockDeviceMapping{{
		DeviceName: aws.String(server.DataDeviceName),
		Ebs:        data,
	}}
}

// tagsFor builds the tags the instance and each of its disks carry.
//
// What gets backed up is the data volume, so the backup tag goes there and nowhere else. Its tags
// end up on its snapshots, which is how a backup still names its server once that server is gone.
// The root disk holds only what the install script can rebuild.
func (p *Provisioner) tagsFor(req createRequest, template Template) (instance, data, root []ec2types.Tag) {
	shared := []ec2types.Tag{
		tag(server.TagServerID, req.ServerID),
		tag(server.TagGameName, req.Game),
		tag(p.config.IDTagName, p.config.IDTagValue),
		tag(p.config.ManagedTag, p.config.ManagedValue),
		tag(server.TagDataPath, template.DataPath),
		tag(server.TagWorldDownloadPaths, template.WorldDownloadPaths),
		tag(server.TagWorldDownloadCorePaths, template.WorldDownloadCorePaths),
	}
	if domain := p.domainFor(req.ServerID); domain != "" {
		shared = append(shared,
			tag(server.TagDomain, domain),
			tag(server.TagHostedZoneID, p.config.HostedZoneID))
	}

	name := "game-server-" + req.ServerID
	instance = append(slices.Clone(shared), tag(server.TagName, name))
	data = append(slices.Clone(shared),
		tag(server.TagName, name+"-data"),
		tag(p.config.BackupTag, p.config.BackupValue))
	root = append(slices.Clone(shared), tag(server.TagName, name+"-root"))
	return instance, data, root
}

// domainFor is the DNS name a server answers on, or "" when the deployment has no hosted zone.
func (p *Provisioner) domainFor(serverID string) string {
	if p.config.HostedZoneName == "" {
		return ""
	}
	return serverID + "." + p.config.HostedZoneName
}

func (p *Provisioner) ami(ctx context.Context) (string, error) {
	out, err := p.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name: aws.String(p.config.AmiParameter),
	})
	if err != nil {
		return "", fmt.Errorf("reading the AMI from %s: %w", p.config.AmiParameter, err)
	}
	if out.Parameter == nil {
		return "", fmt.Errorf("reading the AMI from %s: no value", p.config.AmiParameter)
	}
	return aws.ToString(out.Parameter.Value), nil
}

// installURLs signs the two URLs the installer reports through, so it needs no credentials of its
// own to say how it went.
func (p *Provisioner) installURLs(ctx context.Context, serverID string) (installURLs, error) {
	report, err := p.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.config.StatusBucket),
		Key:         aws.String("status/" + serverID + ".install.json"),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return installURLs{}, fmt.Errorf("signing the install report URL: %w", err)
	}

	logs, err := p.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(p.config.StatusBucket),
		Key:         aws.String("status/" + serverID + ".install.log"),
		ContentType: aws.String("text/plain"),
	})
	if err != nil {
		return installURLs{}, fmt.Errorf("signing the install log URL: %w", err)
	}

	return installURLs{report: report.URL, log: logs.URL}, nil
}
