package control

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"personal-game-server-manager/lambda/internal/games"
	"personal-game-server-manager/lambda/internal/server"
	"personal-game-server-manager/lambda/internal/usererr"
)

// archiveTimeout is how long SSM allows the archive command to run. Its own default of an hour is
// too short for a multi-gigabyte world; its maximum is 48 hours.
const archiveTimeout = 4 * time.Hour

// Download is a world download in progress, which the control panel polls.
type Download struct {
	Status string `json:"status"`
	// CommandID and S3Key identify the archive being built, and are handed back on each poll.
	CommandID string `json:"commandId,omitempty"`
	S3Key     string `json:"s3Key,omitempty"`
	// URL is where to fetch the finished archive.
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// The statuses the control panel reads.
const (
	downloadInProgress = "InProgress"
	downloadSuccess    = "Success"
	downloadFailed     = "Failed"
)

// startWorldDownload archives a world and copies it to S3 for the caller to fetch.
//
// Answers with a handle rather than the usual message, because the archive takes longer than a
// request: the control panel polls getWorldDownloadStatus with what comes back.
func startWorldDownload(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	s, game, err := only(servers)
	if err != nil {
		return failedDownload(err), nil
	}

	scope := games.ParseScope(req.Scope)
	includePaths := s.WorldDownloadPaths
	if scope == games.Core {
		includePaths = s.WorldDownloadCorePaths
	}
	if includePaths == "" {
		return failedDownload(usererr.Newf(
			"%s is missing its world-download-paths tag, so there's no way to tell what to archive",
			s.Label())), nil
	}

	key := worldKey(s, scope)
	commands, err := game.ArchiveWorld(games.ArchiveRequest{
		DataPath:     s.DataPath,
		IncludePaths: includePaths,
		Scope:        scope,
		Bucket:       h.config.StatusBucket,
		Key:          key,
	})
	if err != nil {
		return failedDownload(unsupported(err, "World download", s.GameName)), nil
	}

	commandID, err := h.runner.Start(ctx, s.InstanceID, commands, archiveTimeout)
	if err != nil {
		log.Printf("couldn't start the world download for %s: %v", s.Label(), err)
		return Download{Status: downloadFailed, Error: "Could not start the world download"}, nil
	}
	return Download{Status: downloadInProgress, CommandID: commandID, S3Key: key}, nil
}

// worldKey names the archive. The instance id comes before the scope so that the ownership check
// below is scope-independent.
func worldKey(s server.Server, scope games.Scope) string {
	return fmt.Sprintf("worlds/%s/%s-%s-%d.zip",
		s.GameName, s.InstanceID, scope, time.Now().Unix())
}

// worldDownloadStatus reports on an archive, and signs a URL for it once it is built.
func worldDownloadStatus(ctx context.Context, h *Handler, req Request, servers []server.Server) (any, error) {
	if len(servers) == 0 || req.CommandID == "" || req.S3Key == "" {
		return Download{Status: downloadFailed, Error: "Missing commandId/instanceId/s3Key"}, nil
	}
	s := servers[0]

	// The key came from the client: without this, a caller with download rights on one server could
	// pass another server's key and get a signed URL to a world they can't download.
	if !strings.HasPrefix(req.S3Key, fmt.Sprintf("worlds/%s/%s-", s.GameName, s.InstanceID)) {
		return Download{Status: downloadFailed, Error: "s3Key does not belong to this instance"}, nil
	}

	result, done, err := h.runner.Poll(ctx, req.CommandID, s.InstanceID)
	if err != nil {
		log.Printf("couldn't read the world download for %s: %v", s.Label(), err)
		return Download{Status: downloadFailed, Error: "Could not read the world download"}, nil
	}
	if !done {
		return Download{Status: downloadInProgress}, nil
	}
	if !result.Succeeded {
		return Download{Status: downloadFailed, Error: truncate(result.Stderr, 500)}, nil
	}

	// Signing is local and succeeds whatever the signer's own permissions are - the s3:GetObject grant
	// on this Lambda's role is what makes the URL work when the browser follows it.
	url, err := h.downloads.PresignGetObject(ctx, h.config.StatusBucket, req.S3Key, downloadFilename(req.S3Key, s.InstanceID))
	if err != nil {
		log.Printf("couldn't sign the world download for %s: %v", s.Label(), err)
		return Download{Status: downloadFailed, Error: "Could not sign the download"}, nil
	}
	return Download{Status: downloadSuccess, URL: url}, nil
}

// downloadFilename is what the browser saves the archive as: the game, what was archived and when,
// rather than the instance id and an epoch. Falls back to the key's own last segment.
func downloadFilename(key, instanceID string) string {
	name := key[strings.LastIndex(key, "/")+1:]

	game := "server"
	if parts := strings.Split(key, "/"); len(parts) >= 2 {
		game = parts[1]
	}

	suffix := strings.TrimSuffix(strings.TrimPrefix(name, instanceID+"-"), ".zip")
	scope, epoch, found := strings.Cut(suffix, "-")
	if !found {
		return name
	}
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return name
	}
	return fmt.Sprintf("%s-%s-%s.zip", game, scope, time.Unix(seconds, 0).UTC().Format("20060102-150405"))
}

// failedDownload reports a problem in the shape the control panel's poller expects, since this
// command answers with a handle rather than a message.
func failedDownload(err error) Download {
	message, ok := usererr.Message(err)
	if !ok {
		message = "Could not start the world download"
	}
	return Download{Status: downloadFailed, Error: message}
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit]
}
