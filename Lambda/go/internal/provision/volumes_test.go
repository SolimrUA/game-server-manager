package provision

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"personal-game-server-manager/lambda/internal/server"
)

// volumesOnly answers DescribeVolumes and nothing else, which is all swappableVolume asks of EC2.
type volumesOnly struct {
	EC2
	volumes []ec2types.Volume
}

func (v volumesOnly) DescribeVolumes(_ context.Context, in *ec2.DescribeVolumesInput, _ ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	for _, volume := range v.volumes {
		if aws.ToString(volume.VolumeId) == in.VolumeIds[0] {
			return &ec2.DescribeVolumesOutput{Volumes: []ec2types.Volume{volume}}, nil
		}
	}
	return nil, fmt.Errorf("no volume %s", in.VolumeIds[0])
}

func dataDisk(id, serverID, game string) ec2types.Volume {
	tag := func(k, v string) ec2types.Tag {
		return ec2types.Tag{Key: aws.String(k), Value: aws.String(v)}
	}
	return ec2types.Volume{
		VolumeId:         aws.String(id),
		AvailabilityZone: aws.String("eu-central-1c"),
		Tags: []ec2types.Tag{
			tag("managed-by", "control-panel"),
			tag(server.TagServerID, serverID),
			tag(server.TagGameName, game),
		},
	}
}

func provisionerWith(volumes ...ec2types.Volume) *Provisioner {
	return &Provisioner{
		ec2:    volumesOnly{volumes: volumes},
		config: Config{ManagedTag: "managed-by", ManagedValue: "control-panel"},
	}
}

func stoppedServer(serverID, game string) server.Server {
	return server.Server{ServerID: serverID, GameName: game, State: "stopped",
		AvailabilityZone: "eu-central-1c", InstanceID: "i-1", DataVolumeID: "vol-00000000000000001"}
}

func TestSwappableVolumeRefusesAnotherGamesWorld(t *testing.T) {
	disk := dataDisk("vol-0962efecaa32e74ef", "vs-test2", "vintagestory")
	p := provisionerWith(disk)

	_, err := p.swappableVolume(context.Background(), stoppedServer("vs-test2", "valheim"), "vol-0962efecaa32e74ef")
	if err == nil {
		t.Fatal("a Vintage Story world was accepted by a Valheim server")
	}
	if !strings.Contains(err.Error(), "vintagestory") || !strings.Contains(err.Error(), "valheim") {
		t.Errorf("the refusal names neither game: %v", err)
	}
}

func TestSwappableVolumeRefusesAWorldWithNoGameRecorded(t *testing.T) {
	disk := dataDisk("vol-0962efecaa32e74ef", "vs-test2", "")
	p := provisionerWith(disk)

	if _, err := p.swappableVolume(context.Background(), stoppedServer("vs-test2", "valheim"), "vol-0962efecaa32e74ef"); err == nil {
		t.Fatal("a world of unknown game was accepted")
	}
}

func TestSwappableVolumeTakesTheSameGamesWorld(t *testing.T) {
	disk := dataDisk("vol-0962efecaa32e74ef", "vs-test2", "valheim")
	p := provisionerWith(disk)

	if _, err := p.swappableVolume(context.Background(), stoppedServer("vs-test2", "valheim"), "vol-0962efecaa32e74ef"); err != nil {
		t.Fatalf("a world of the server's own game was refused: %v", err)
	}
}

func TestSortedWorldsKeepsEachGamesWorldsTogether(t *testing.T) {
	worlds := map[string]*WorldVolume{
		"a": {VolumeID: "a", ServerID: "vs-test2", Game: "vintagestory",
			Backups: []WorldBackup{{StartTime: "2026-11-01T00:00:00Z"}}},
		"b": {VolumeID: "b", ServerID: "vs-test2", Game: "valheim",
			Backups: []WorldBackup{{StartTime: "2026-11-09T00:00:00Z"}}},
		"c": {VolumeID: "c", ServerID: "vs-test2", Game: "vintagestory",
			Backups: []WorldBackup{{StartTime: "2026-11-08T00:00:00Z"}}},
	}

	got := ""
	for _, world := range sortedWorlds(worlds) {
		got += world.Game + "/" + world.VolumeID + " "
	}
	want := "valheim/b vintagestory/c vintagestory/a "
	if got != want {
		t.Errorf("worlds came out as %q, wanted %q", got, want)
	}
}
