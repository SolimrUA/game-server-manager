package provision

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// worldPrefix names the parameter holding the world a server generates next. The instance reads it
// before the game starts, and no parameter leaves the world it already has.
const worldPrefix = "game-world-"

// SetWorld records the world a server's game generates when it next starts. Nothing here touches the
// running server, and the save it is already on stays on the volume.
func (p *Provisioner) SetWorld(ctx context.Context, serverID, world string) error {
	_, err := p.ssm.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(worldPrefix + serverID),
		Value:     aws.String(world),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("storing the world for %s: %w", serverID, err)
	}
	return nil
}
