package provision

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/status"
	"personal-game-server-manager/lambda/internal/usererr"
)

// serverIDPattern constrains a server id, which names its DNS record, its join-password parameter
// and its status object.
var serverIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

// minVolumeSizeGib is the smallest data volume a server can be given. A server restored from a
// backup takes its size from the snapshot instead.
const minVolumeSizeGib = 8

// EC2 is the instances and volumes the control panel creates.
type EC2 interface {
	RunInstances(context.Context, *ec2.RunInstancesInput, ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	DescribeSnapshots(context.Context, *ec2.DescribeSnapshotsInput, ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error)
	CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
}

// SSM holds the join passwords and the AMI to launch.
type SSM interface {
	GetParameter(context.Context, *ssm.GetParameterInput, ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
	PutParameter(context.Context, *ssm.PutParameterInput, ...func(*ssm.Options)) (*ssm.PutParameterOutput, error)
}

// Presigner signs the URLs the installer reports to, which it uses instead of credentials of its own.
type Presigner interface {
	PresignPutObject(context.Context, *s3.PutObjectInput, ...func(*s3.PresignOptions)) (*PresignedURL, error)
}

// PresignedURL is a signed request the installer can follow without credentials.
type PresignedURL struct {
	URL string
}

// Launcher starts the steps that run once an instance exists, such as naming it in DNS.
type Launcher interface {
	AfterLaunch(ctx context.Context, instanceID, domainName, hostedZoneID string) error
}

// Config is what the control panel was deployed with.
type Config struct {
	Templates Templates

	SubnetID           string
	InstanceProfileArn string
	AmiParameter       string
	StatusBucket       string

	ManagedTag   string
	ManagedValue string
	BackupTag    string
	BackupValue  string
	IDTagName    string
	IDTagValue   string

	HostedZoneID   string
	HostedZoneName string

	MaxServers           int
	AllowedInstanceTypes []string
}

// Provisioner creates and removes game servers.
type Provisioner struct {
	ec2       EC2
	ssm       SSM
	status    *status.Reader
	presigner Presigner
	launcher  Launcher
	config    Config
}

func New(ec2Client EC2, ssmClient SSM, statusReader *status.Reader, presigner Presigner,
	launcher Launcher, config Config) *Provisioner {

	return &Provisioner{
		ec2:       ec2Client,
		ssm:       ssmClient,
		status:    statusReader,
		presigner: presigner,
		launcher:  launcher,
		config:    config,
	}
}

// Backup is a snapshot a new server can be created from.
type Backup struct {
	SnapshotID string `json:"snapshotId"`
	ServerID   string `json:"serverId"`
	Game       string `json:"game"`
	SizeGib    int32  `json:"sizeGib"`
	StartTime  string `json:"startTime"`
}

// Options is everything the new-server form needs.
type Options struct {
	Templates     []Option `json:"templates"`
	Backups       []Backup `json:"backups"`
	InstanceTypes []string `json:"instanceTypes"`
	MaxServers    int      `json:"maxServers"`
}

// Options lists what a new server can be created as, and from.
func (p *Provisioner) Options(ctx context.Context) Options {
	backups, err := p.backups(ctx)
	if err != nil {
		// The form is still usable for a fresh world, which is worth more than an error page.
		log.Printf("couldn't list backups: %v", err)
	}

	return Options{
		Templates:     p.config.Templates.Options(),
		Backups:       backups,
		InstanceTypes: p.config.AllowedInstanceTypes,
		MaxServers:    p.config.MaxServers,
	}
}

// backups lists each server's newest backup. The ones behind it exist to put a server back to an
// earlier point, which is a different thing to ask for than starting a new server.
func (p *Provisioner) backups(ctx context.Context) ([]Backup, error) {
	out, err := p.ec2.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		OwnerIds: []string{"self"},
		Filters:  p.backupFilters(),
	})
	if err != nil {
		return nil, err
	}

	snapshots := out.Snapshots
	sort.Slice(snapshots, func(i, j int) bool {
		return aws.ToTime(snapshots[i].StartTime).After(aws.ToTime(snapshots[j].StartTime))
	})

	backups := []Backup{}
	seen := map[string]bool{}
	for _, snapshot := range snapshots {
		backup := asBackup(snapshot)
		// A snapshot with no server-id tag stands for itself rather than hiding another server's.
		key := backup.ServerID
		if key == "" {
			key = backup.SnapshotID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		backups = append(backups, backup)
	}
	return backups, nil
}

func asBackup(snapshot ec2types.Snapshot) Backup {
	tags := map[string]string{}
	for _, tag := range snapshot.Tags {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return Backup{
		SnapshotID: aws.ToString(snapshot.SnapshotId),
		ServerID:   tags[server.TagServerID],
		Game:       tags[server.TagGameName],
		SizeGib:    aws.ToInt32(snapshot.VolumeSize),
		StartTime:  aws.ToTime(snapshot.StartTime).UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func (p *Provisioner) backupFilters() []ec2types.Filter {
	return []ec2types.Filter{
		{Name: aws.String("tag:" + p.config.BackupTag), Values: []string{p.config.BackupValue}},
		{Name: aws.String("status"), Values: []string{string(ec2types.SnapshotStateCompleted)}},
	}
}

// backup reads one snapshot, and confirms it is a backup this control panel took.
func (p *Provisioner) backup(ctx context.Context, snapshotID string) (*Backup, error) {
	out, err := p.ec2.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		SnapshotIds: []string{snapshotID},
		Filters:     p.backupFilters(),
	})
	if err != nil || len(out.Snapshots) == 0 {
		return nil, usererr.New("That backup isn't available - it may have been deleted")
	}
	backup := asBackup(out.Snapshots[0])
	return &backup, nil
}

// existing lists the servers this control panel created, in any state short of terminated.
func (p *Provisioner) existing(ctx context.Context) ([]server.Server, error) {
	out, err := p.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + p.config.ManagedTag), Values: []string{p.config.ManagedValue}},
			{Name: aws.String("instance-state-name"),
				Values: []string{"pending", "running", "stopping", "stopped"}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("listing the existing servers: %w", err)
	}

	var servers []server.Server
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			servers = append(servers, server.From(instance))
		}
	}
	return servers, nil
}

// Delete terminates a server. Its data volume outlives it, and its backups are what a later server
// of the same world is created from.
func (p *Provisioner) Delete(ctx context.Context, s server.Server) (string, error) {
	_, err := p.ec2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{s.InstanceID},
	})
	if err != nil {
		return "", fmt.Errorf("terminating %s: %w", s.InstanceID, err)
	}

	log.Printf("deleted server %s (%s), data volume %s", s.Label(), s.InstanceID, s.DataVolumeID)
	return fmt.Sprintf("Terminated %s - create a server from its newest backup to bring this world back.",
		s.Label()), nil
}

// tagVolumes names both of a new server's disks after it and what they hold, so a volume on its own
// says where it belongs.
func (p *Provisioner) tagVolumes(ctx context.Context, instanceID string, dataTags, rootTags []ec2types.Tag) (string, error) {
	dataVolumeID, rootVolumeIDs, err := p.awaitVolumes(ctx, instanceID)
	if err != nil {
		return "", err
	}

	if _, err := p.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{dataVolumeID},
		Tags:      dataTags,
	}); err != nil {
		return "", fmt.Errorf("naming the data volume of %s: %w", instanceID, err)
	}

	if len(rootVolumeIDs) == 0 {
		log.Printf("no root volume to name on %s", instanceID)
		return dataVolumeID, nil
	}
	if _, err := p.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: rootVolumeIDs,
		Tags:      rootTags,
	}); err != nil {
		// The data volume is the one that has to carry its tags, since they are how it is backed up.
		log.Printf("couldn't name the root volume of %s: %v", instanceID, err)
	}
	return dataVolumeID, nil
}

// awaitVolumes waits for a new instance's disks to appear. The ids exist as soon as the instance
// does, but the mappings take a moment, and the two disks are attached a moment apart - so this
// waits for both, giving up on the root disk alone once the attempts run out.
func (p *Provisioner) awaitVolumes(ctx context.Context, instanceID string) (string, []string, error) {
	const attempts = 10
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", nil, ctx.Err()
			case <-time.After(time.Second):
			}
		}

		dataVolumeID, rootVolumeIDs, err := p.volumesOf(ctx, instanceID)
		if err != nil {
			return "", nil, err
		}
		if dataVolumeID != "" && (len(rootVolumeIDs) > 0 || attempt == attempts-1) {
			return dataVolumeID, rootVolumeIDs, nil
		}
	}
	return "", nil, fmt.Errorf("no data volume appeared on %s", instanceID)
}

// volumesOf reports an instance's data disk and whatever else is attached to it.
func (p *Provisioner) volumesOf(ctx context.Context, instanceID string) (string, []string, error) {
	out, err := p.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{instanceID}})
	if err != nil {
		return "", nil, fmt.Errorf("describing %s: %w", instanceID, err)
	}

	var dataVolumeID string
	var rootVolumeIDs []string
	for _, mapping := range blockDevicesOf(out) {
		if mapping.Ebs == nil {
			continue
		}
		volumeID := aws.ToString(mapping.Ebs.VolumeId)
		switch {
		case aws.ToString(mapping.DeviceName) == server.DataDeviceName:
			dataVolumeID = volumeID
		case volumeID != "":
			rootVolumeIDs = append(rootVolumeIDs, volumeID)
		}
	}
	return dataVolumeID, rootVolumeIDs, nil
}

// blockDevicesOf flattens a describe of a single instance down to its disks.
func blockDevicesOf(out *ec2.DescribeInstancesOutput) []ec2types.InstanceBlockDeviceMapping {
	var mappings []ec2types.InstanceBlockDeviceMapping
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			mappings = append(mappings, instance.BlockDeviceMappings...)
		}
	}
	return mappings
}

func tag(key, value string) ec2types.Tag {
	return ec2types.Tag{Key: aws.String(key), Value: aws.String(value)}
}

// nameOf reads the Name a set of tags gives, for a log line about the server being created.
func nameOf(tags []ec2types.Tag) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == server.TagName {
			return aws.ToString(t.Value)
		}
	}
	return ""
}
