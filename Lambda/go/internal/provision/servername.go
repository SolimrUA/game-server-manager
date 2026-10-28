package provision

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// serverNamePrefix names a server's display-name parameter. The instance reads it before the game
// starts, and no parameter leaves the game's own name alone.
const serverNamePrefix = "game-servername-"

// SetServerName records the name a server's game takes on when it next starts; nothing here touches
// the running server.
func (p *Provisioner) SetServerName(ctx context.Context, serverID, name string) error {
	_, err := p.ssm.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(serverNamePrefix + serverID),
		Value:     aws.String(name),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return fmt.Errorf("storing the name for %s: %w", serverID, err)
	}
	return nil
}
