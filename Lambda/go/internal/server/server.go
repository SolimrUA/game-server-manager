// Package server describes a game server as the control panel sees it: an EC2 instance whose tags
// say which server it is, what it runs and where that game keeps its files.
package server

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// The tags a server carries. Everything the control panel needs about a server is read from these
// rather than taken from the caller, so a request can only ever name a server, never describe one.
const (
	TagServerID               = "server-id"
	TagGameName               = "game-name"
	TagDataPath               = "data-path"
	TagWorldDownloadPaths     = "world-download-paths"
	TagWorldDownloadCorePaths = "world-download-core-paths"
	TagDomain                 = "domain"
	TagHostedZoneID           = "hostedZoneId"
	TagName                   = "Name"

	// TagIdleCheck records that one idle check already found nobody playing, so the next one stops the
	// server. It lives on the instance because the check runs on a schedule with no memory of its own.
	TagIdleCheck = "idle-check"
	// TagIdlePause keeps a server up whatever its player count, e.g. while working on it.
	TagIdlePause   = "idle-shutdown"
	IdlePauseValue = "paused"
)

// DataDeviceName is where a server's data volume attaches. The install scripts find the volume by
// looking for the one disk that isn't the root, so this only has to be free.
const DataDeviceName = "/dev/sdf"

// Tags indexes an instance's tags by key.
type Tags map[string]string

func TagsOf(instance ec2types.Instance) Tags {
	tags := make(Tags, len(instance.Tags))
	for _, tag := range instance.Tags {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return tags
}

// Server is one game server. The JSON names are the control panel's: the front end reads these
// fields directly.
type Server struct {
	InstanceID   string `json:"InstanceId"`
	InstanceType string `json:"InstanceType"`
	State        string `json:"State"`

	ServerID string `json:"ServerId"`
	GameName string `json:"GameName"`
	DataPath string `json:"DataPath"`

	WorldDownloadPaths     string `json:"WorldDownloadPaths"`
	WorldDownloadCorePaths string `json:"WorldDownloadCorePaths"`

	DomainName      string `json:"DomainName"`
	HostedZoneID    string `json:"hostedZoneId"`
	PublicIPAddress string `json:"PublicIpAddress"`

	DataVolumeID string `json:"DataVolumeId"`

	Capabilities []string `json:"Capabilities"`

	GameStatus         GameStatus `json:"GameStatus"`
	InstallStatus      string     `json:"InstallStatus"`
	ServerName         string     `json:"ServerName"`
	LastBackupTime     string     `json:"LastBackupTime"`
	IdleShutdownStatus string     `json:"IdleShutdownStatus"`
}

// The strings the front end shows when a server carries no such tag.
const (
	noDomain     = "No domain tag found"
	noHostedZone = "No hosted zone tag found"
	noPublicIP   = "No public IP address"
)

// From reads the parts of a server that come from the instance itself. What needs another service to
// answer - its status, its backups - is filled in by the caller.
func From(instance ec2types.Instance) Server {
	tags := TagsOf(instance)

	s := Server{
		InstanceID:             aws.ToString(instance.InstanceId),
		InstanceType:           string(instance.InstanceType),
		ServerID:               tags[TagServerID],
		GameName:               tags[TagGameName],
		DataPath:               tags[TagDataPath],
		WorldDownloadPaths:     tags[TagWorldDownloadPaths],
		WorldDownloadCorePaths: tags[TagWorldDownloadCorePaths],
		DomainName:             noDomain,
		HostedZoneID:           noHostedZone,
		PublicIPAddress:        noPublicIP,
		DataVolumeID:           dataVolumeOf(instance),
		IdleShutdownStatus:     tags.IdleShutdownStatus(),
	}
	if instance.State != nil {
		s.State = string(instance.State.Name)
	}
	if domain := tags[TagDomain]; domain != "" {
		s.DomainName = domain
	}
	if zone := tags[TagHostedZoneID]; zone != "" {
		s.HostedZoneID = zone
	}
	if ip := aws.ToString(instance.PublicIpAddress); ip != "" {
		s.PublicIPAddress = ip
	}
	return s
}

// HasDomain reports whether this server has a DNS record to point at its address.
func (s Server) HasDomain() bool {
	return s.DomainName != "" && s.DomainName != noDomain
}

// StatusKey names the server's status object. A server with no server-id tag is keyed on its game.
func (s Server) StatusKey() string {
	if s.ServerID != "" {
		return s.ServerID
	}
	return s.GameName
}

// Label is the server's name for a message to the caller, falling back as far as the instance id so
// that a half-tagged server is still named by something.
func (s Server) Label() string {
	for _, candidate := range []string{s.ServerID, s.GameName, s.InstanceID} {
		if candidate != "" {
			return candidate
		}
	}
	return "server"
}

// IdleShutdownStatus says whether idle shutdown applies to a server and how far along it is. One
// schedule covers every server, so a server is exempted by tag rather than by its own rule.
func (t Tags) IdleShutdownStatus() string {
	switch {
	case t[TagIdlePause] == IdlePauseValue:
		return "disabled"
	case t[TagIdleCheck] == "true":
		return "triggered-once"
	default:
		return "enabled"
	}
}

func dataVolumeOf(instance ec2types.Instance) string {
	for _, mapping := range instance.BlockDeviceMappings {
		if aws.ToString(mapping.DeviceName) == DataDeviceName && mapping.Ebs != nil {
			return aws.ToString(mapping.Ebs.VolumeId)
		}
	}
	return ""
}
