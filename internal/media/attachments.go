package media

import (
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/deicod/gotodon/internal/db"
)

// AttachmentFileURL returns the URL of a media attachment's file: the remote
// URL of an attachment from another server, otherwise the URL of the stored
// file. It is "" when neither is known.
func AttachmentFileURL(files Storage, attachment db.MediaAttachment) string {
	if remote := strings.TrimSpace(attachment.RemoteUrl); remote != "" {
		if IsAbsoluteURL(remote) {
			return remote
		}
		return ""
	}
	return FileURL(files, attachmentStoragePath(attachment, attachment.FileFileName))
}

// AttachmentThumbnailURL returns the URL of a media attachment's thumbnail:
// its remote thumbnail, otherwise the stored one. It is "" for attachments
// without a thumbnail.
func AttachmentThumbnailURL(files Storage, attachment db.MediaAttachment) string {
	if remote := strings.TrimSpace(attachment.ThumbnailRemoteUrl.String); attachment.ThumbnailRemoteUrl.Valid && IsAbsoluteURL(remote) {
		return remote
	}
	return FileURL(files, attachmentStoragePath(attachment, attachment.ThumbnailFileName))
}

// attachmentStoragePath returns the storage path of one of a local
// attachment's files, or "" when the attachment lacks what the path needs.
func attachmentStoragePath(attachment db.MediaAttachment, filename pgtype.Text) string {
	if !attachment.AccountID.Valid || !attachment.CreatedAt.Valid || !filename.Valid {
		return ""
	}
	return MediaStoragePath(attachment.AccountID.Int64, attachment.CreatedAt.Time, strings.TrimSpace(filename.String))
}
