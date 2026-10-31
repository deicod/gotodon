package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/deicod/gotodon/pkg/safehttp"
)

// MaxDocumentBytes bounds ActivityPub and WebFinger documents fetched from
// remote servers.
const MaxDocumentBytes = 1 << 20

// FetchDocument GETs the ActivityStreams document at uri with client and
// decodes it into v. It fails for responses other than 200, for media types
// other than application/activity+json and application/ld+json, and for
// bodies over MaxDocumentBytes. Pass a safehttp client for URIs that remote
// servers supplied.
func FetchDocument(ctx context.Context, client *http.Client, uri string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", ActivityPubContentType+", "+ActivityPubLDContentType)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("returned status %d", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if mediaType := parseMediaType(contentType); mediaType != ActivityPubContentType && mediaType != "application/ld+json" {
		return fmt.Errorf("returned content type %q", contentType)
	}
	body, err := safehttp.ReadBody(resp, MaxDocumentBytes)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

// parseMediaType returns the lower-cased media type of a Content-Type header
// without its parameters, or "" if the header is malformed.
func parseMediaType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return mediaType
}

// isJSONMediaType reports whether mediaType is application/json or a +json
// structured syntax type such as application/jrd+json.
func isJSONMediaType(mediaType string) bool {
	return mediaType == "application/json" ||
		(strings.HasPrefix(mediaType, "application/") && strings.HasSuffix(mediaType, "+json"))
}
