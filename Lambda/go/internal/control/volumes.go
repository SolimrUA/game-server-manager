package control

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/backup"
	backuptypes "github.com/aws/aws-sdk-go-v2/service/backup/types"

	"personal-game-server-manager/lambda/internal/provision"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// maxExpiryDays is the longest retention the backup vault accepts.
const maxExpiryDays = 36500

// listWorlds answers the Backups page, with each snapshot's expiry filled in from the vault.
func listWorlds(ctx context.Context, h *Handler, _ Request, servers []server.Server) (any, error) {
	worlds, err := h.provisioner.Worlds(ctx)
	if err != nil {
		return nil, err
	}

	expiries, err := h.expiries(ctx)
	if err != nil {
		// The page is still worth showing without them.
		log.Printf("couldn't read backup expiries: %v", err)
	}
	states := map[string]string{}
	for _, s := range servers {
		states[s.ServerID] = s.State
	}
	for i := range worlds {
		worlds[i].ServerState = states[worlds[i].ServerID]
		for j := range worlds[i].Backups {
			worlds[i].Backups[j].ExpiresAt = expiries[worlds[i].Backups[j].SnapshotID]
		}
	}
	return worlds, nil
}

// expiries is when each snapshot is due to be deleted, by snapshot id. One listing, since the page
// shows every volume at once.
func (h *Handler) expiries(ctx context.Context) (map[string]string, error) {
	expiries := map[string]string{}
	paginator := backup.NewListRecoveryPointsByBackupVaultPaginator(h.backup,
		&backup.ListRecoveryPointsByBackupVaultInput{BackupVaultName: aws.String(h.config.BackupVaultName)})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return expiries, fmt.Errorf("listing recovery points: %w", err)
		}
		for _, point := range page.RecoveryPoints {
			id := provision.SnapshotOfARN(aws.ToString(point.RecoveryPointArn))
			if id == "" || point.CalculatedLifecycle == nil || point.CalculatedLifecycle.DeleteAt == nil {
				continue
			}
			expiries[id] = aws.ToTime(point.CalculatedLifecycle.DeleteAt).UTC().Format(time.RFC3339)
		}
	}
	return expiries, nil
}

// setBackupExpiry puts a snapshot on a clock, or takes it off one.
func setBackupExpiry(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	arn, err := h.provisioner.SnapshotARN(req.SnapshotID)
	if err != nil {
		return nil, err
	}

	days := req.Body.ExpiryDays
	if days < 0 || days > maxExpiryDays {
		return nil, usererr.Newf("An expiry is between 1 and %d days away, or none at all", maxExpiryDays)
	}

	// -1 is how the vault is told to stop deleting this one at all; an empty lifecycle is rejected,
	// and zero is not a retention it accepts.
	lifecycle := &backuptypes.Lifecycle{DeleteAfterDays: aws.Int64(-1)}
	if days > 0 {
		lifecycle.DeleteAfterDays = aws.Int64(int64(days))
	}
	if _, err := h.backup.UpdateRecoveryPointLifecycle(ctx, &backup.UpdateRecoveryPointLifecycleInput{
		BackupVaultName:  aws.String(h.config.BackupVaultName),
		RecoveryPointArn: aws.String(arn),
		Lifecycle:        lifecycle,
	}); err != nil {
		// The vault says only "Lifecycle cannot affect past".
		if strings.Contains(err.Error(), "cannot affect past") {
			return nil, usererr.Newf("%d days after it was taken is already in the past, so that "+
				"backup would have to have gone already. Give it more days than it is old.", days)
		}
		return nil, fmt.Errorf("changing when %s expires: %w", req.SnapshotID, err)
	}

	if days == 0 {
		return "Expiry removed from " + req.SnapshotID, nil
	}
	// A backup on its way out is not one to create a server from.
	if err := h.provisioner.SetBackupPrimary(ctx, req.SnapshotID, false); err != nil {
		log.Printf("%v", err)
	}
	return fmt.Sprintf("%s expires %d days after it was taken, and is now Defunct",
		req.SnapshotID, days), nil
}

func setBackupPrimary(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	if err := h.provisioner.SetBackupPrimary(ctx, req.SnapshotID, req.Body.Primary); err != nil {
		return nil, err
	}
	return req.SnapshotID + " is now " + statusWord(req.Body.Primary), nil
}

func statusWord(primary bool) string {
	if primary {
		return "Primary"
	}
	return "Defunct"
}

// useVolume moves a server onto another of its volumes. The volume's tag names the server.
func useVolume(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	target, err := h.serverHolding(ctx, req.VolumeID, servers)
	if err != nil {
		return nil, err
	}
	if err := h.provisioner.UseVolume(ctx, target, req.VolumeID); err != nil {
		return nil, err
	}
	return fmt.Sprintf("%s is now on %s", target.Label(), req.VolumeID), nil
}

// serverHolding is the server a world belongs to, named by the volume's own server-id tag.
func (h *Handler) serverHolding(ctx context.Context, volumeID string, servers []server.Server) (server.Server, error) {
	worlds, err := h.provisioner.Worlds(ctx)
	if err != nil {
		return server.Server{}, err
	}
	for _, world := range worlds {
		if world.VolumeID != volumeID {
			continue
		}
		for _, s := range servers {
			if s.ServerID == world.ServerID {
				return s, nil
			}
		}
		return server.Server{}, usererr.Newf("%s has no server to put this world on", world.ServerID)
	}
	return server.Server{}, usererr.New("No such world")
}

// retireVolume stops nightly backups of a volume nothing is running on, which has to happen before
// the disk can be reclaimed.
func retireVolume(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	if err := h.provisioner.RetireVolume(ctx, req.VolumeID); err != nil {
		return nil, err
	}
	return "Stopped backing up " + req.VolumeID, nil
}

// deleteVolume reclaims the disk a retired world was on.
func deleteVolume(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	if err := h.provisioner.DeleteVolume(ctx, req.VolumeID); err != nil {
		return nil, err
	}
	return "Deleted " + req.VolumeID, nil
}

// deleteVolumeBackups removes every backup of one volume. Nothing brings them back.
func deleteVolumeBackups(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	snapshots, err := h.provisioner.BackupsOf(ctx, req.VolumeID)
	if err != nil {
		return nil, err
	}
	if len(snapshots) == 0 {
		return nil, usererr.New("That world has no backups to delete")
	}
	// Asked again rather than refused: the vault can accept a delete and not carry it out, and asking
	// again is the only way back from that.
	requested := time.Now().UTC().Format(time.RFC3339)
	var failed []string
	for _, id := range snapshots {
		if err := h.deleteBackup(ctx, id); err != nil {
			log.Printf("couldn't delete %s: %v", id, err)
			failed = append(failed, id)
			continue
		}
		if err := h.provisioner.MarkDeleteRequested(ctx, id, requested); err != nil {
			// The delete is already asked for, so this only costs the page its record of when.
			log.Printf("%v", err)
		}
	}
	if len(failed) > 0 {
		return nil, usererr.Newf("Submitted %d backups of %s for deletion, but %d were refused",
			len(snapshots), req.VolumeID, len(failed))
	}

	// The vault removes the snapshots in its own time, and they stay listed until it has.
	return fmt.Sprintf("Submitted for deletion: all %d backups of %s", len(snapshots), req.VolumeID), nil
}

func deleteOneBackup(ctx context.Context, h *Handler, req Request, _ []server.Server) (any, error) {
	backup, err := h.backupNamed(ctx, req.SnapshotID)
	if err != nil {
		return nil, err
	}
	if backup.Primary {
		return nil, usererr.New("That backup is primary, so a server can still be created from it. " +
			"Make it defunct first.")
	}

	if err := h.deleteBackup(ctx, req.SnapshotID); err != nil {
		return nil, fmt.Errorf("deleting %s: %w", req.SnapshotID, err)
	}
	if err := h.provisioner.MarkDeleteRequested(ctx, req.SnapshotID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Printf("%v", err)
	}
	return "Submitted for deletion: " + req.SnapshotID, nil
}

func (h *Handler) backupNamed(ctx context.Context, snapshotID string) (provision.WorldBackup, error) {
	worlds, err := h.provisioner.Worlds(ctx)
	if err != nil {
		return provision.WorldBackup{}, err
	}
	for _, world := range worlds {
		for _, backup := range world.Backups {
			if backup.SnapshotID == snapshotID {
				return backup, nil
			}
		}
	}
	return provision.WorldBackup{}, usererr.New("No such backup")
}

// deleteBackup hands a snapshot to the vault, the only thing that can remove one it manages.
func (h *Handler) deleteBackup(ctx context.Context, snapshotID string) error {
	arn, err := h.provisioner.SnapshotARN(snapshotID)
	if err != nil {
		return err
	}
	_, err = h.backup.DeleteRecoveryPoint(ctx, &backup.DeleteRecoveryPointInput{
		BackupVaultName:  aws.String(h.config.BackupVaultName),
		RecoveryPointArn: aws.String(arn),
	})
	return err
}
