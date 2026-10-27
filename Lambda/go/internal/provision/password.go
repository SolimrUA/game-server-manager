package provision

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// passwordPrefix names a server's join-password parameter. The installer reads it, so it has to
// hold the right value before the instance boots.
const passwordPrefix = "game-password-"

// PasswordSource says where a new server's join password came from.
type PasswordSource string

const (
	// PasswordKept means this server id already had one, so players keep the password they have.
	PasswordKept PasswordSource = "kept"
	// PasswordInherited means it was copied from the server a backup was taken from.
	PasswordInherited PasswordSource = "inherited"
	// PasswordNew means one was generated.
	PasswordNew PasswordSource = "new"
)

func passwordName(serverID string) string {
	return passwordPrefix + serverID
}

// ensurePassword makes sure a server id has a join password, and says where it came from.
//
// A backup carries the configuration of the server it was taken from, which holds that server's
// password. Copying it keeps the stored parameter and the running game in agreement - without it a
// restored world would answer to a password nobody has.
func (p *Provisioner) ensurePassword(ctx context.Context, serverID, inheritFrom string) (PasswordSource, error) {
	if existing, _ := p.readPassword(ctx, serverID); existing != "" {
		return PasswordKept, nil
	}

	password, source := "", PasswordNew
	if inheritFrom != "" {
		if inherited, _ := p.readPassword(ctx, inheritFrom); inherited != "" {
			password, source = inherited, PasswordInherited
		}
	}
	if password == "" {
		generated, err := newPassword()
		if err != nil {
			return "", err
		}
		password = generated
	}

	_, err := p.ssm.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(passwordName(serverID)),
		Value:     aws.String(password),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		return "", fmt.Errorf("storing the join password for %s: %w", serverID, err)
	}
	return source, nil
}

func (p *Provisioner) readPassword(ctx context.Context, serverID string) (string, error) {
	out, err := p.ssm.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(passwordName(serverID)),
		WithDecryption: aws.Bool(true),
	})
	if err != nil {
		return "", err
	}
	if out.Parameter == nil {
		return "", nil
	}
	return aws.ToString(out.Parameter.Value), nil
}

// newPassword returns a password players type into the game's join dialog, so it is hex rather than
// anything needing an escape.
func newPassword() (string, error) {
	buf := make([]byte, 15)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generating a join password: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
