package activitypub

import (
	"fmt"
	"strings"
)

// InstanceActorURI returns the ActivityPub ID of the instance actor of the
// server at domain, https://<domain>/actor as in Mastodon's
// instance_actor_url. It returns "" for a blank domain, which has no
// instance actor.
func InstanceActorURI(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	return fmt.Sprintf("https://%s/actor", domain)
}
