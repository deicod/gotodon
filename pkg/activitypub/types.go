// Package activitypub provides ActivityPub protocol implementation.
package activitypub

import "strings"

// Context constants for ActivityPub
const (
	ActivityStreamsContext = "https://www.w3.org/ns/activitystreams"
	SecurityContext        = "https://w3id.org/security/v1"
	MastodonContext        = "https://joinmastodon.org/ns"
)

// Common activity types
const (
	TypeCreate   = "Create"
	TypeUpdate   = "Update"
	TypeDelete   = "Delete"
	TypeFollow   = "Follow"
	TypeAccept   = "Accept"
	TypeReject   = "Reject"
	TypeUndo     = "Undo"
	TypeLike     = "Like"
	TypeAnnounce = "Announce"
	TypeBlock    = "Block"
	TypeFlag     = "Flag"
	TypeAdd      = "Add"
	TypeRemove   = "Remove"
	TypeMove     = "Move"
)

// Actor types
const (
	TypePerson       = "Person"
	TypeApplication  = "Application"
	TypeGroup        = "Group"
	TypeOrganization = "Organization"
	TypeService      = "Service"
)

// Object types
const (
	TypeNote                  = "Note"
	TypeArticle               = "Article"
	TypeImage                 = "Image"
	TypeVideo                 = "Video"
	TypeDocument              = "Document"
	TypeAudio                 = "Audio"
	TypeCollection            = "Collection"
	TypeOrderedCollection     = "OrderedCollection"
	TypeCollectionPage        = "CollectionPage"
	TypeOrderedCollectionPage = "OrderedCollectionPage"
)

// Object represents a base ActivityPub object
type Object struct {
	Context      any         `json:"@context,omitempty"`
	ID           string      `json:"id,omitempty"`
	Type         string      `json:"type"`
	Name         string      `json:"name,omitempty"`
	Content      string      `json:"content,omitempty"`
	Summary      string      `json:"summary,omitempty"`
	Published    string      `json:"published,omitempty"`
	Updated      string      `json:"updated,omitempty"`
	URL          any         `json:"url,omitempty"`
	AttributedTo any         `json:"attributedTo,omitempty"`
	To           []string    `json:"to,omitempty"`
	Cc           []string    `json:"cc,omitempty"`
	Bto          []string    `json:"bto,omitempty"`
	Bcc          []string    `json:"bcc,omitempty"`
	InReplyTo    any         `json:"inReplyTo,omitempty"`
	Tag          []Tag       `json:"tag,omitempty"`
	Attachment   []any       `json:"attachment,omitempty"`
	Sensitive    bool        `json:"sensitive,omitempty"`
	Replies      *Collection `json:"replies,omitempty"`
}

// Activity represents an ActivityPub activity
type Activity struct {
	Context        any      `json:"@context,omitempty"`
	ID             string   `json:"id,omitempty"`
	Type           string   `json:"type"`
	Published      string   `json:"published,omitempty"`
	To             []string `json:"to,omitempty"`
	Cc             []string `json:"cc,omitempty"`
	Actor          any      `json:"actor"`
	ActivityObject any      `json:"object,omitempty"`
	Target         any      `json:"target,omitempty"`
	Result         any      `json:"result,omitempty"`
	Origin         any      `json:"origin,omitempty"`
}

// Actor represents an ActivityPub actor (Person, Application, etc.)
type Actor struct {
	Object
	PreferredUsername         string     `json:"preferredUsername,omitempty"`
	Inbox                     string     `json:"inbox"`
	Outbox                    string     `json:"outbox"`
	Following                 string     `json:"following,omitempty"`
	Followers                 string     `json:"followers,omitempty"`
	Featured                  string     `json:"featured,omitempty"`
	FeaturedTags              string     `json:"featuredTags,omitempty"`
	Endpoints                 *Endpoints `json:"endpoints,omitempty"`
	PublicKey                 *PublicKey `json:"publicKey,omitempty"`
	Icon                      *Image     `json:"icon,omitempty"`
	Image                     *Image     `json:"image,omitempty"`
	ManuallyApprovesFollowers bool       `json:"manuallyApprovesFollowers,omitempty"`
	Discoverable              *bool      `json:"discoverable,omitempty"`
	Indexable                 *bool      `json:"indexable,omitempty"`
	Memorial                  bool       `json:"memorial,omitempty"`
	AlsoKnownAs               []string   `json:"alsoKnownAs,omitempty"`
	MovedTo                   string     `json:"movedTo,omitempty"`
}

// Endpoints represents actor endpoints
type Endpoints struct {
	SharedInbox string `json:"sharedInbox,omitempty"`
}

// PublicKey represents an actor's public key
type PublicKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
}

// Image represents an image object
type Image struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType,omitempty"`
	URL       string `json:"url"`
}

// Tag represents a tag (mention, hashtag, emoji)
type Tag struct {
	Type string `json:"type"`
	Href string `json:"href,omitempty"`
	Name string `json:"name"`
	Icon *Image `json:"icon,omitempty"`
}

// Collection represents an ActivityPub collection
type Collection struct {
	Object
	TotalItems int   `json:"totalItems"`
	First      any   `json:"first,omitempty"`
	Last       any   `json:"last,omitempty"`
	Current    any   `json:"current,omitempty"`
	Items      []any `json:"items,omitempty"`
}

// OrderedCollection represents an ordered ActivityPub collection
type OrderedCollection struct {
	Collection
	OrderedItems []any `json:"orderedItems,omitempty"`
}

// CollectionPage represents a page in a collection
type CollectionPage struct {
	Collection
	PartOf any    `json:"partOf,omitempty"`
	Next   string `json:"next,omitempty"`
	Prev   string `json:"prev,omitempty"`
}

// OrderedCollectionPage represents a page in an ordered collection.
type OrderedCollectionPage struct {
	Collection
	OrderedItems []any  `json:"orderedItems,omitempty"`
	PartOf       any    `json:"partOf,omitempty"`
	Next         string `json:"next,omitempty"`
	Prev         string `json:"prev,omitempty"`
}

// Public audience constant
const PublicAudience = "https://www.w3.org/ns/activitystreams#Public"

// DefaultContext returns the standard ActivityPub context
func DefaultContext() []any {
	return []any{
		ActivityStreamsContext,
		SecurityContext,
	}
}

// MastodonContextFull returns the full Mastodon-compatible context
func MastodonContextFull() []any {
	return []any{
		ActivityStreamsContext,
		SecurityContext,
		map[string]any{
			"manuallyApprovesFollowers": "as:manuallyApprovesFollowers",
			"toot":                      MastodonContext,
			"featured":                  map[string]string{"@id": "toot:featured", "@type": "@id"},
			"featuredTags":              map[string]string{"@id": "toot:featuredTags", "@type": "@id"},
			"alsoKnownAs":               map[string]string{"@id": "as:alsoKnownAs", "@type": "@id"},
			"movedTo":                   map[string]string{"@id": "as:movedTo", "@type": "@id"},
			"schema":                    "http://schema.org#",
			"discoverable":              "toot:discoverable",
			"suspended":                 "toot:suspended",
			"indexable":                 "toot:indexable",
			"memorial":                  "toot:memorial",
			"Emoji":                     "toot:Emoji",
			"Hashtag":                   "as:Hashtag",
			"PropertyValue":             "schema:PropertyValue",
			"value":                     "schema:value",
			"attributionDomains":        map[string]string{"@id": "toot:attributionDomains", "@type": "@id"},
		},
	}
}

// IsPublic checks if an activity targets the public audience
func IsPublic(to []string, cc []string) bool {
	for _, a := range to {
		if a == PublicAudience {
			return true
		}
	}
	for _, a := range cc {
		if a == PublicAudience {
			return true
		}
	}
	return false
}

// ExtractDomain extracts the domain from an actor URI
func ExtractDomain(uri string) string {
	// Remove protocol
	uri = strings.TrimPrefix(uri, "https://")
	uri = strings.TrimPrefix(uri, "http://")
	// Get domain
	if idx := strings.Index(uri, "/"); idx > 0 {
		return uri[:idx]
	}
	return uri
}
