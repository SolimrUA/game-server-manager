// Command backupservers backs up each game server that has changed since its last backup, and keeps
// only the most recent few backups of each. Named a server, it backs up that one alone.
//
// AWS Backup plans can only schedule unconditionally and expire by age, so the decision and the
// pruning live here instead.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// server is one game server's data volume, which is what holds its world and so what is backed up.
type server struct {
	VolumeID   string
	ServerID   string
	LaunchTime time.Time
}

// gameStatus is the part of a server's status object that says when its world was last written.
type gameStatus struct {
	LastSaveTime string `json:"lastSaveTime"`
}

type handler struct {
	ec2    *ec2.Client
	backup *backup.Client
	s3     *s3.Client

	vaultName      string
	roleArn        string
	statusBucket   string
	backupTag      string
	backupValue    string
	keepPerServer  int
	supersededDays int64
	accountID      string
	region         string
}

func main() {
	ctx := context.Background()
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("loading AWS config: %v", err)
	}

	keep, err := strconv.Atoi(os.Getenv("keepPerServer"))
	if err != nil || keep < 1 {
		log.Fatalf("keepPerServer must be a positive number, got %q", os.Getenv("keepPerServer"))
	}

	superseded, err := strconv.ParseInt(os.Getenv("supersededDays"), 10, 64)
	if err != nil || superseded < 1 {
		log.Fatalf("supersededDays must be a positive number, got %q", os.Getenv("supersededDays"))
	}

	h := &handler{
		ec2:            ec2.NewFromConfig(cfg),
		backup:         backup.NewFromConfig(cfg),
		s3:             s3.NewFromConfig(cfg),
		vaultName:      os.Getenv("backupVaultName"),
		roleArn:        os.Getenv("backupRoleArn"),
		statusBucket:   os.Getenv("statusBucket"),
		backupTag:      os.Getenv("backupTag"),
		backupValue:    os.Getenv("backupValue"),
		keepPerServer:  keep,
		supersededDays: superseded,
		accountID:      os.Getenv("accountId"),
		region:         cfg.Region,
	}
	lambda.Start(h.handle)
}

// request is empty for the nightly sweep. Backup Now names a server and forces a backup, because
// pressing the button has to produce one whether or not the world has changed since the last.
type request struct {
	ServerID string `json:"serverId"`
	Force    bool   `json:"force"`
}

func (h *handler) handle(ctx context.Context, req request) (string, error) {
	servers, err := h.servers(ctx)
	if err != nil {
		return "", err
	}

	if req.ServerID != "" {
		named := servers[:0]
		for _, s := range servers {
			if s.ServerID == req.ServerID {
				named = append(named, s)
			}
		}
		if len(named) == 0 {
			return "", fmt.Errorf("no data volume is tagged for server %q", req.ServerID)
		}
		servers = named
	}

	var failed []string
	for _, s := range servers {
		if err := h.backUpIfChanged(ctx, s, req.Force); err != nil {
			//One server's failure must not stop the others being backed up or pruned.
			log.Printf("%s: %v", s.ServerID, err)
			failed = append(failed, s.ServerID)
		}
	}
	if len(failed) > 0 {
		return "", fmt.Errorf("backup failed for %s", strings.Join(failed, ", "))
	}
	return "Backup started for " + strings.Join(serverIDs(servers), ", "), nil
}

func serverIDs(servers []server) []string {
	ids := make([]string, 0, len(servers))
	for _, s := range servers {
		ids = append(ids, s.ServerID)
	}
	return ids
}

func (h *handler) servers(ctx context.Context) ([]server, error) {
	var found []server
	paginator := ec2.NewDescribeVolumesPaginator(h.ec2, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + h.backupTag), Values: []string{h.backupValue}},
		},
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing server volumes: %w", err)
		}
		for _, volume := range page.Volumes {
			found = append(found, h.serverOn(ctx, volume))
		}
	}
	return found, nil
}

// serverOn describes the server a data volume belongs to.
func (h *handler) serverOn(ctx context.Context, volume ec2types.Volume) server {
	s := server{VolumeID: aws.ToString(volume.VolumeId)}
	for _, tag := range volume.Tags {
		if aws.ToString(tag.Key) == "server-id" {
			s.ServerID = aws.ToString(tag.Value)
		}
	}

	//The volume keeps changing only while a server is running on it, so that server's start time is
	//one of the two things that says whether this world has moved on.
	for _, attachment := range volume.Attachments {
		launched, err := h.instanceLaunchTime(ctx, aws.ToString(attachment.InstanceId))
		if err != nil {
			log.Printf("%s: %v", s.ServerID, err)
			continue
		}
		s.LaunchTime = launched
	}
	return s
}

func (h *handler) instanceLaunchTime(ctx context.Context, instanceID string) (time.Time, error) {
	if instanceID == "" {
		return time.Time{}, nil
	}
	described, err := h.ec2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("reading %s: %w", instanceID, err)
	}
	for _, reservation := range described.Reservations {
		for _, instance := range reservation.Instances {
			if instance.LaunchTime != nil {
				return *instance.LaunchTime, nil
			}
		}
	}
	return time.Time{}, nil
}

func (h *handler) volumeArn(volumeID string) string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:volume/%s", h.region, h.accountID, volumeID)
}

func (h *handler) backUpIfChanged(ctx context.Context, s server, force bool) error {
	points, err := h.recoveryPoints(ctx, s)
	if err != nil {
		return err
	}

	var newest time.Time
	if len(points) > 0 {
		newest = aws.ToTime(points[0].CreationDate)
	}

	reason := "asked for"
	if !force {
		reason = h.changeSince(ctx, s, newest)
	}
	if reason != "" {
		log.Printf("%s: backing up - %s", s.ServerID, reason)
		if _, err := h.backup.StartBackupJob(ctx, &backup.StartBackupJobInput{
			BackupVaultName: aws.String(h.vaultName),
			ResourceArn:     aws.String(h.volumeArn(s.VolumeID)),
			IamRoleArn:      aws.String(h.roleArn),
		}); err != nil {
			return fmt.Errorf("starting backup: %w", err)
		}
		//Only the newest backup of a server is kept for good, so the one this supersedes starts its
		//clock now. A server nobody deletes loses them to the prune first; a deleted one keeps its
		//newest and lets the rest run out.
		if len(points) > 0 {
			if err := h.expire(ctx, s, points[0]); err != nil {
				log.Printf("%s: %v", s.ServerID, err)
			}
		}
		//Counted as if it already existed, so the prune leaves room for it rather than keeping one too
		//many until the next run.
		points = append([]backuptypes.RecoveryPointByBackupVault{{}}, points...)
	} else {
		log.Printf("%s: unchanged since %s, skipping", s.ServerID, newest.Format(time.RFC3339))
	}

	return h.prune(ctx, s, points)
}

// changeSince reports why this server is worth backing up again, or "" when nothing has changed.
func (h *handler) changeSince(ctx context.Context, s server, newest time.Time) string {
	if newest.IsZero() {
		return "no backup yet"
	}
	if s.LaunchTime.After(newest) {
		return "started at " + s.LaunchTime.Format(time.RFC3339)
	}

	//A server that runs for days keeps the same launch time while its world goes on changing, so the
	//game's own save time is what tells those days apart.
	lastSave, err := h.lastSaveTime(ctx, s.ServerID)
	if err != nil {
		//Backed up rather than skipped: an unreadable status object says nothing about the world.
		return "status unreadable: " + err.Error()
	}
	if lastSave.After(newest) {
		return "world saved at " + lastSave.Format(time.RFC3339)
	}
	return ""
}

func (h *handler) lastSaveTime(ctx context.Context, serverID string) (time.Time, error) {
	if serverID == "" {
		return time.Time{}, fmt.Errorf("no server-id tag")
	}
	object, err := h.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(h.statusBucket),
		Key:    aws.String("status/" + serverID + ".json"),
	})
	if err != nil {
		return time.Time{}, err
	}
	defer object.Body.Close()

	var status gameStatus
	if err := json.NewDecoder(object.Body).Decode(&status); err != nil {
		return time.Time{}, err
	}
	if status.LastSaveTime == "" {
		return time.Time{}, fmt.Errorf("no lastSaveTime reported")
	}
	return time.Parse(time.RFC3339, status.LastSaveTime)
}

// recoveryPoints returns this server's completed recovery points, newest first.
func (h *handler) recoveryPoints(ctx context.Context, s server) ([]backuptypes.RecoveryPointByBackupVault, error) {
	var points []backuptypes.RecoveryPointByBackupVault
	paginator := backup.NewListRecoveryPointsByBackupVaultPaginator(h.backup,
		&backup.ListRecoveryPointsByBackupVaultInput{
			BackupVaultName: aws.String(h.vaultName),
			ByResourceArn:   aws.String(h.volumeArn(s.VolumeID)),
		})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing recovery points: %w", err)
		}
		for _, point := range page.RecoveryPoints {
			//Only points that can actually be restored count towards what is kept: one already expiring
			//would otherwise take a place and leave fewer than asked for.
			if point.Status == backuptypes.RecoveryPointStatusCompleted {
				points = append(points, point)
			}
		}
	}

	sort.Slice(points, func(i, j int) bool {
		return aws.ToTime(points[i].CreationDate).After(aws.ToTime(points[j].CreationDate))
	})
	return points, nil
}

// expire gives a superseded backup a limited life. AWS Backup counts the lifecycle from when the point
// was taken, so reaching the intended date means asking for its own age plus the window.
func (h *handler) expire(ctx context.Context, s server, point backuptypes.RecoveryPointByBackupVault) error {
	arn := aws.ToString(point.RecoveryPointArn)
	//A point with no expiry still carries a lifecycle, with nothing in it, so the date is what says
	//whether one has already been set.
	alreadySet := point.CalculatedLifecycle != nil && point.CalculatedLifecycle.DeleteAt != nil
	if arn == "" || alreadySet {
		return nil
	}

	days := int64(time.Since(aws.ToTime(point.CreationDate)).Hours()/24) + h.supersededDays
	log.Printf("%s: %s superseded, expiring in %d days", s.ServerID, arn, h.supersededDays)
	_, err := h.backup.UpdateRecoveryPointLifecycle(ctx, &backup.UpdateRecoveryPointLifecycleInput{
		BackupVaultName:  aws.String(h.vaultName),
		RecoveryPointArn: aws.String(arn),
		Lifecycle:        &backuptypes.Lifecycle{DeleteAfterDays: aws.Int64(days)},
	})
	if err != nil {
		return fmt.Errorf("expiring %s: %w", arn, err)
	}
	return nil
}

// prune keeps the newest keepPerServer points. Backups started here carry no lifecycle, so nothing
// else ever removes them.
func (h *handler) prune(ctx context.Context, s server, points []backuptypes.RecoveryPointByBackupVault) error {
	if len(points) <= h.keepPerServer {
		return nil
	}
	var failed error
	for _, point := range points[h.keepPerServer:] {
		arn := aws.ToString(point.RecoveryPointArn)
		if arn == "" {
			continue
		}
		log.Printf("%s: deleting %s from %s", s.ServerID, arn, aws.ToTime(point.CreationDate).Format(time.RFC3339))
		if _, err := h.backup.DeleteRecoveryPoint(ctx, &backup.DeleteRecoveryPointInput{
			BackupVaultName:  aws.String(h.vaultName),
			RecoveryPointArn: aws.String(arn),
		}); err != nil {
			//Carries on through the rest: one point that refuses to go must not pin every older one.
			failed = errors.Join(failed, fmt.Errorf("deleting %s: %w", arn, err))
		}
	}
	return failed
}
