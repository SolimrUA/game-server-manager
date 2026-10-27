package control

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"personal-game-server-manager/lambda/internal/access"
	"personal-game-server-manager/lambda/internal/provision"
	"personal-game-server-manager/lambda/internal/remote"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/status"
)

// EC2 is the instances the control panel acts on.
type EC2 interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	ModifyInstanceAttribute(context.Context, *ec2.ModifyInstanceAttributeInput, ...func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error)
	CreateTags(context.Context, *ec2.CreateTagsInput, ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error)
	DeleteTags(context.Context, *ec2.DeleteTagsInput, ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error)
}

// Backup reports when each server was last backed up.
type Backup interface {
	ListRecoveryPointsByBackupVault(context.Context, *backup.ListRecoveryPointsByBackupVaultInput, ...func(*backup.Options)) (*backup.ListRecoveryPointsByBackupVaultOutput, error)
}

// Backups invokes the Lambda that takes a backup, so that taking one, expiring the one it supersedes
// and pruning the rest happen the same way however they were asked for.
type Backups interface {
	Now(ctx context.Context, serverID string) (string, error)
}

// Downloads signs the URL a finished world archive is fetched from.
type Downloads interface {
	PresignGetObject(ctx context.Context, bucket, key, filename string) (string, error)
}

// Config is what the control Lambda was deployed with.
type Config struct {
	TagName  string
	TagValue string

	StatusBucket    string
	BackupVaultName string
	Region          string
	AccountID       string

	// InstanceTypesBySize maps the sizes the resize action offers to instance types. The allowlist the
	// Lambda's own IAM policy conditions RunInstances on has to agree with these.
	InstanceTypesBySize map[string]string
}

type Handler struct {
	ec2         EC2
	backup      Backup
	backups     Backups
	status      *status.Reader
	runner      *remote.Runner
	downloads   Downloads
	grants      *access.Store
	directory   *access.Directory
	provisioner *provision.Provisioner
	launcher    provision.Launcher
	config      Config
}

// Dependencies is everything the handler acts through.
type Dependencies struct {
	EC2         EC2
	Backup      Backup
	Backups     Backups
	Status      *status.Reader
	Runner      *remote.Runner
	Downloads   Downloads
	Grants      *access.Store
	Directory   *access.Directory
	Provisioner *provision.Provisioner
	Launcher    provision.Launcher
	Config      Config
}

func NewHandler(deps Dependencies) *Handler {
	return &Handler{
		ec2:         deps.EC2,
		backup:      deps.Backup,
		backups:     deps.Backups,
		status:      deps.Status,
		runner:      deps.Runner,
		downloads:   deps.Downloads,
		grants:      deps.Grants,
		directory:   deps.Directory,
		provisioner: deps.Provisioner,
		launcher:    deps.Launcher,
		config:      deps.Config,
	}
}

// visible lists the servers the caller may see, with everything the control panel shows about each.
func (h *Handler) visible(ctx context.Context, req Request, who caller) ([]server.Server, error) {
	tagName, tagValue := req.TagName, req.TagValue
	if tagName == "" {
		tagName, tagValue = h.config.TagName, h.config.TagValue
	}

	out, err := h.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{{Name: aws.String("tag:" + tagName), Values: []string{tagValue}}},
	})
	if err != nil {
		return nil, fmt.Errorf("listing servers: %w", err)
	}

	// Never nil: the control panel iterates this, and an empty fleet is an ordinary state.
	servers := []server.Server{}
	var launchedAt []time.Time
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			if instance.State != nil && instance.State.Name == ec2types.InstanceStateNameTerminated {
				continue
			}

			s := server.From(instance)
			// Resolved before anything is looked up for this server, so one the caller can't see costs
			// no further calls either.
			capabilities := access.For(who.isAdmin, who.roles[s.ServerID])
			if !capabilities.Has(access.View) {
				continue
			}
			s.Capabilities = capabilities.Strings()

			servers = append(servers, s)
			launchedAt = append(launchedAt, aws.ToTime(instance.LaunchTime))
		}
	}

	// Each server's lookups are independent, and the control panel polls this often enough that doing
	// them one after another would show up as lag.
	var wait sync.WaitGroup
	for i := range servers {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			h.describe(ctx, &servers[i], launchedAt[i])
		}(i)
	}
	wait.Wait()

	return servers, nil
}

// describe fills in what needs another service to answer: what the game reports about itself, how
// its install went, and when it was last backed up.
//
// None of these is worth failing the request over - a server whose status can't be read is still
// one the caller can start and stop - so each is logged and left empty.
func (h *Handler) describe(ctx context.Context, s *server.Server, launchedAt time.Time) {
	if s.State == string(ec2types.InstanceStateNameRunning) {
		game, err := h.status.Game(ctx, s.StatusKey(), launchedAt)
		if err != nil {
			log.Printf("no game status for %s: %v", s.Label(), err)
		}
		s.GameStatus = game
		s.ServerName = game.ServerName()
	}

	// Only looked up when the game isn't reporting: a server that answers has plainly finished
	// installing, and this is polled every few seconds.
	if s.GameStatus == nil {
		install, err := h.status.Install(ctx, s.StatusKey())
		if err != nil {
			log.Printf("no install status for %s: %v", s.Label(), err)
		}
		s.InstallStatus = install
	}

	lastBackup, err := h.lastBackup(ctx, s.DataVolumeID)
	if err != nil {
		log.Printf("no backup info for %s: %v", s.Label(), err)
	}
	s.LastBackupTime = lastBackup
}

// lastBackup reports when a volume was last backed up. Every server shares one vault, so a recovery
// point is identified by the volume it was taken from - asking by vault alone would report any
// server's backup as this one's.
func (h *Handler) lastBackup(ctx context.Context, volumeID string) (string, error) {
	if h.config.BackupVaultName == "" || volumeID == "" {
		return "", nil
	}

	out, err := h.backup.ListRecoveryPointsByBackupVault(ctx, &backup.ListRecoveryPointsByBackupVaultInput{
		BackupVaultName: aws.String(h.config.BackupVaultName),
		ByResourceArn: aws.String(fmt.Sprintf("arn:aws:ec2:%s:%s:volume/%s",
			h.config.Region, h.config.AccountID, volumeID)),
	})
	if err != nil {
		return "", err
	}

	var newest time.Time
	for _, point := range out.RecoveryPoints {
		if point.Status != backuptypes.RecoveryPointStatusCompleted {
			continue
		}
		if created := aws.ToTime(point.CreationDate); created.After(newest) {
			newest = created
		}
	}
	if newest.IsZero() {
		return "", nil
	}
	return newest.UTC().Format("2006-01-02T15:04:05Z"), nil
}
