package provision

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// WorldVolume is a data volume a server has held, with the backups taken from it. The volume may be
// gone and its backups still there.
type WorldVolume struct {
	VolumeID string `json:"volumeId"`
	ServerID string `json:"serverId"`
	Game     string `json:"game"`
	SizeGib  int32  `json:"sizeGib"`
	// State is the volume's own state, or "deleted" when only its backups are left.
	State string `json:"state"`
	// Created is left empty for a volume that no longer exists.
	Created    string `json:"created"`
	InstanceID string `json:"instanceId"`
	// BackedUp reports whether new backups are still being taken from this volume.
	BackedUp bool `json:"backedUp"`
	// ServerState is empty when the volume's server no longer exists.
	ServerState string        `json:"serverState"`
	Backups     []WorldBackup `json:"backups"`
}

// WorldBackup is one snapshot of a world.
type WorldBackup struct {
	SnapshotID string `json:"snapshotId"`
	StartTime  string `json:"startTime"`
	SizeGib    int32  `json:"sizeGib"`
	Primary    bool   `json:"primary"`
	// DeleteRequested is when the vault was asked to remove this snapshot, empty when it has not been.
	DeleteRequested string `json:"deleteRequested"`
	// ExpiresAt comes from the backup vault, and is empty when nothing will delete this snapshot.
	ExpiresAt string `json:"expiresAt"`
}

// Worlds lists every data volume the control panel made, newest snapshot first within each.
func (p *Provisioner) Worlds(ctx context.Context) ([]WorldVolume, error) {
	snapshots, err := p.ec2.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		OwnerIds: []string{"self"},
		Filters:  p.backupFilters(),
	})
	if err != nil {
		return nil, fmt.Errorf("listing backups: %w", err)
	}

	worlds := map[string]*WorldVolume{}
	for _, snapshot := range snapshots.Snapshots {
		addSnapshot(worlds, snapshot)
	}

	live, err := p.dataVolumes(ctx)
	if err != nil {
		return nil, err
	}
	for id, volume := range live {
		if worlds[id] == nil {
			// Empty rather than nil, since the page walks it.
			worlds[id] = &WorldVolume{VolumeID: id, Backups: []WorldBackup{},
				ServerID: tagOf(volume.Tags, server.TagServerID),
				Game:     tagOf(volume.Tags, server.TagGameName)}
		}
		describe(worlds[id], volume, p.config.BackupTag, p.config.BackupValue)
	}

	return sortedWorlds(worlds), nil
}

// addSnapshot files a snapshot under the volume it was taken from. Its own tags are the only record
// of which server that was, once the volume is gone.
func addSnapshot(worlds map[string]*WorldVolume, snapshot ec2types.Snapshot) {
	backup := asBackup(snapshot)
	id := backup.VolumeID
	if id == "" {
		id = backup.SnapshotID
	}
	if worlds[id] == nil {
		worlds[id] = &WorldVolume{
			VolumeID: backup.VolumeID,
			ServerID: backup.ServerID,
			Game:     backup.Game,
			SizeGib:  backup.SizeGib,
			// Overwritten once the volume itself is found.
			State: "deleted",
		}
	}
	worlds[id].Backups = append(worlds[id].Backups, WorldBackup{
		SnapshotID:      backup.SnapshotID,
		StartTime:       backup.StartTime,
		SizeGib:         backup.SizeGib,
		Primary:         backup.Primary,
		DeleteRequested: backup.DeleteRequested,
	})
}

func describe(world *WorldVolume, volume ec2types.Volume, backupTag, backupValue string) {
	world.State = string(volume.State)
	world.SizeGib = aws.ToInt32(volume.Size)
	world.Created = aws.ToTime(volume.CreateTime).UTC().Format(time.RFC3339)
	world.BackedUp = tagOf(volume.Tags, backupTag) == backupValue
	if len(volume.Attachments) > 0 {
		world.InstanceID = aws.ToString(volume.Attachments[0].InstanceId)
	}
}

// dataVolumes are every data volume the control panel made, backed up or not. Told from the root
// disk by where it attaches, because the two carry the same tags.
func (p *Provisioner) dataVolumes(ctx context.Context) (map[string]ec2types.Volume, error) {
	out, err := p.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{
		Filters: []ec2types.Filter{
			{Name: aws.String("tag:" + p.config.ManagedTag), Values: []string{p.config.ManagedValue}},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("listing data volumes: %w", err)
	}

	volumes := map[string]ec2types.Volume{}
	for _, volume := range out.Volumes {
		if holdsAWorld(volume) {
			volumes[aws.ToString(volume.VolumeId)] = volume
		}
	}
	return volumes, nil
}

// holdsAWorld reports whether a volume is a data disk. A root disk never outlives its instance, so
// an unattached volume is always a data one.
func holdsAWorld(volume ec2types.Volume) bool {
	for _, attachment := range volume.Attachments {
		return aws.ToString(attachment.Device) == server.DataDeviceName
	}
	return true
}

func sortedWorlds(worlds map[string]*WorldVolume) []WorldVolume {
	sorted := make([]WorldVolume, 0, len(worlds))
	for _, world := range worlds {
		sort.Slice(world.Backups, func(i, j int) bool {
			return world.Backups[i].StartTime > world.Backups[j].StartTime
		})
		sorted = append(sorted, *world)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].ServerID != sorted[j].ServerID {
			return sorted[i].ServerID < sorted[j].ServerID
		}
		// Worlds of the same game sort together, since one server id can have held worlds under more
		// than one game.
		if sorted[i].Game != sorted[j].Game {
			return sorted[i].Game < sorted[j].Game
		}
		return newestOf(sorted[i]) > newestOf(sorted[j])
	})
	return sorted
}

func newestOf(world WorldVolume) string {
	if len(world.Backups) == 0 {
		return ""
	}
	return world.Backups[0].StartTime
}

func tagOf(tags []ec2types.Tag, key string) string {
	for _, t := range tags {
		if aws.ToString(t.Key) == key {
			return aws.ToString(t.Value)
		}
	}
	return ""
}

// SetBackupPrimary decides whether a snapshot is one a server can be created from.
func (p *Provisioner) SetBackupPrimary(ctx context.Context, snapshotID string, primary bool) error {
	if err := checkSnapshotID(snapshotID); err != nil {
		return err
	}

	ids := []string{snapshotID}
	tags := []ec2types.Tag{{Key: aws.String(DefunctTag), Value: aws.String("true")}}
	var err error
	if primary {
		_, err = p.ec2.DeleteTags(ctx, &ec2.DeleteTagsInput{Resources: ids, Tags: tags})
	} else {
		_, err = p.ec2.CreateTags(ctx, &ec2.CreateTagsInput{Resources: ids, Tags: tags})
	}
	if err != nil {
		return fmt.Errorf("changing the status of %s: %w", snapshotID, err)
	}
	return nil
}

// RetireVolume stops a volume being backed up, which is what deleting its server does for it.
func (p *Provisioner) RetireVolume(ctx context.Context, volumeID string) error {
	volume, err := p.dataVolume(ctx, volumeID)
	if err != nil {
		return err
	}
	if len(volume.Attachments) > 0 {
		return usererr.New("That volume is attached to a server. Delete the server first.")
	}
	return p.retireVolume(ctx, volumeID)
}

// DeleteVolume reclaims a data volume. It does not touch the snapshots taken from it, which are
// deleted separately.
func (p *Provisioner) DeleteVolume(ctx context.Context, volumeID string) error {
	volume, err := p.dataVolume(ctx, volumeID)
	if err != nil {
		return err
	}
	if len(volume.Attachments) > 0 {
		return usererr.New("That volume is attached to a server. Delete the server first.")
	}
	if tagOf(volume.Tags, p.config.BackupTag) == p.config.BackupValue {
		return usererr.New("That volume is still the live world of a server, so it can't be deleted.")
	}

	if _, err := p.ec2.DeleteVolume(ctx, &ec2.DeleteVolumeInput{VolumeId: aws.String(volumeID)}); err != nil {
		return fmt.Errorf("deleting volume %s: %w", volumeID, err)
	}
	return nil
}

// dataVolume reads one volume, and confirms it is one this control panel made.
func (p *Provisioner) dataVolume(ctx context.Context, volumeID string) (ec2types.Volume, error) {
	if !volumeIDPattern.MatchString(volumeID) {
		return ec2types.Volume{}, usererr.New("That isn't a volume id")
	}

	out, err := p.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{volumeID}})
	if err != nil {
		return ec2types.Volume{}, fmt.Errorf("reading volume %s: %w", volumeID, err)
	}
	if len(out.Volumes) == 0 {
		return ec2types.Volume{}, usererr.New("No such volume")
	}

	volume := out.Volumes[0]
	if tagOf(volume.Tags, p.config.ManagedTag) != p.config.ManagedValue {
		return ec2types.Volume{}, usererr.New("That volume wasn't made by this control panel")
	}
	return volume, nil
}

// BackupsOf lists the snapshots taken from one volume.
func (p *Provisioner) BackupsOf(ctx context.Context, volumeID string) ([]string, error) {
	if !volumeIDPattern.MatchString(volumeID) {
		return nil, usererr.New("That isn't a volume id")
	}

	filters := append(p.backupFilters(),
		ec2types.Filter{Name: aws.String("volume-id"), Values: []string{volumeID}})
	out, err := p.ec2.DescribeSnapshots(ctx, &ec2.DescribeSnapshotsInput{
		OwnerIds: []string{"self"},
		Filters:  filters,
	})
	if err != nil {
		return nil, fmt.Errorf("listing the backups of %s: %w", volumeID, err)
	}

	ids := make([]string, 0, len(out.Snapshots))
	for _, snapshot := range out.Snapshots {
		ids = append(ids, aws.ToString(snapshot.SnapshotId))
	}
	return ids, nil
}

// MarkDeleteRequested records that the vault has been asked to remove a snapshot.
func (p *Provisioner) MarkDeleteRequested(ctx context.Context, snapshotID, when string) error {
	if err := checkSnapshotID(snapshotID); err != nil {
		return err
	}
	_, err := p.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{snapshotID},
		Tags:      []ec2types.Tag{{Key: aws.String(deleteRequestedTag), Value: aws.String(when)}},
	})
	if err != nil {
		return fmt.Errorf("recording the delete of %s: %w", snapshotID, err)
	}
	return nil
}

func checkSnapshotID(snapshotID string) error {
	if !snapshotIDPattern.MatchString(snapshotID) {
		return usererr.New("That isn't a snapshot id")
	}
	return nil
}

// SnapshotARN names a snapshot the way the backup vault records it.
func (p *Provisioner) SnapshotARN(snapshotID string) (string, error) {
	if err := checkSnapshotID(snapshotID); err != nil {
		return "", err
	}
	return fmt.Sprintf("arn:aws:ec2:%s::snapshot/%s", p.config.Region, snapshotID), nil
}

// SnapshotOfARN is the snapshot a recovery point stands for.
func SnapshotOfARN(arn string) string {
	_, id, found := strings.Cut(arn, ":snapshot/")
	if !found {
		return ""
	}
	return id
}

// UseVolume puts a stopped server onto another of the volumes it has held. Only the disk changes:
// the server keeps its name, address and join password.
func (p *Provisioner) UseVolume(ctx context.Context, s server.Server, volumeID string) error {
	wanted, err := p.swappableVolume(ctx, s, volumeID)
	if err != nil {
		return err
	}
	if aws.ToString(wanted.VolumeId) == s.DataVolumeID {
		return usererr.New("That server is already on this world")
	}

	if err := p.detachData(ctx, s); err != nil {
		return err
	}
	if _, err := p.ec2.AttachVolume(ctx, &ec2.AttachVolumeInput{
		InstanceId: aws.String(s.InstanceID),
		VolumeId:   aws.String(volumeID),
		Device:     aws.String(server.DataDeviceName),
	}); err != nil {
		return fmt.Errorf("attaching %s to %s: %w", volumeID, s.InstanceID, err)
	}

	// The backup tag follows the volume in use. Marked before the old one is retired, so a failure
	// here leaves a volume still backed up rather than none.
	_, err = p.ec2.CreateTags(ctx, &ec2.CreateTagsInput{
		Resources: []string{volumeID},
		Tags:      []ec2types.Tag{{Key: aws.String(p.config.BackupTag), Value: aws.String(p.config.BackupValue)}},
	})
	if err != nil {
		return fmt.Errorf("marking %s for backup: %w", volumeID, err)
	}
	if err := p.retireVolume(ctx, s.DataVolumeID); err != nil {
		log.Printf("%s: %v", s.Label(), err)
	}
	return nil
}

// swappableVolume is a volume a server may be moved onto: this panel's, free, and in its zone.
func (p *Provisioner) swappableVolume(ctx context.Context, s server.Server, volumeID string) (ec2types.Volume, error) {
	if s.State != "stopped" {
		return ec2types.Volume{}, usererr.New("Stop the server before changing which world it is on")
	}

	volume, err := p.dataVolume(ctx, volumeID)
	if err != nil {
		return ec2types.Volume{}, err
	}
	if len(volume.Attachments) > 0 {
		return ec2types.Volume{}, usererr.New("That world is attached to a server already")
	}
	if tagOf(volume.Tags, server.TagServerID) != s.ServerID {
		return ec2types.Volume{}, usererr.New("That world belongs to a different server")
	}
	// A server id outlives the server, so the same id can come back running another game, which
	// cannot read the world on this disk.
	if game := tagOf(volume.Tags, server.TagGameName); game != s.GameName {
		return ec2types.Volume{}, usererr.Newf("That world is %s's and this server runs %s",
			gameLabel(game), gameLabel(s.GameName))
	}
	if aws.ToString(volume.AvailabilityZone) != s.AvailabilityZone {
		return ec2types.Volume{}, usererr.Newf("That world is in %s and the server is in %s, so it can't be "+
			"attached. Create a server from one of its backups instead.",
			aws.ToString(volume.AvailabilityZone), s.AvailabilityZone)
	}
	return volume, nil
}

func gameLabel(game string) string {
	if game == "" {
		return "no recorded game"
	}
	return game
}

// detachData waits for the disk to come free, since nothing can be attached in its place until it is.
func (p *Provisioner) detachData(ctx context.Context, s server.Server) error {
	if s.DataVolumeID == "" {
		return nil
	}
	if _, err := p.ec2.DetachVolume(ctx, &ec2.DetachVolumeInput{
		VolumeId:   aws.String(s.DataVolumeID),
		InstanceId: aws.String(s.InstanceID),
	}); err != nil {
		return fmt.Errorf("detaching %s from %s: %w", s.DataVolumeID, s.InstanceID, err)
	}
	return p.awaitAvailable(ctx, s.DataVolumeID)
}

func (p *Provisioner) awaitAvailable(ctx context.Context, volumeID string) error {
	for i := 0; i < 30; i++ {
		out, err := p.ec2.DescribeVolumes(ctx, &ec2.DescribeVolumesInput{VolumeIds: []string{volumeID}})
		if err != nil {
			return fmt.Errorf("reading volume %s: %w", volumeID, err)
		}
		if len(out.Volumes) > 0 && out.Volumes[0].State == ec2types.VolumeStateAvailable {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("volume %s did not come free", volumeID)
}
