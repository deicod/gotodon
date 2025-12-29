package media

// Processing state values match Mastodon media_attachments.processing enum.
const (
	ProcessingQueued     int32 = 0
	ProcessingInProgress int32 = 1
	ProcessingComplete   int32 = 2
	ProcessingFailed     int32 = 3
)
