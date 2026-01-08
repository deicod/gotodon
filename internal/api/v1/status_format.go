package v1

import (
	"bytes"
	stdhtml "html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	xhtml "golang.org/x/net/html"
	"golang.org/x/net/idna"

	"github.com/deicod/gotodon/internal/config"
	"github.com/deicod/gotodon/internal/db"
)

type statusContentOptions struct {
	author   *Account
	mentions []Mention
	tags     []Tag
	quote    *Status
}

func formatStatusContent(cfg *config.Config, status db.Status, opts statusContentOptions) string {
	local := !status.Local.Valid || status.Local.Bool
	return formatStatusText(cfg, local, status.Text, opts)
}

func formatStatusText(cfg *config.Config, local bool, text string, opts statusContentOptions) string {
	if text == "" {
		return ""
	}
	if !local {
		return sanitizeStatusHTML(text)
	}
	html := linkifyStatusText(cfg, text, opts)
	html = simpleFormatHTML(html)
	html = addQuoteFallback(html, opts.quote)
	return html
}

type entityKind int

const (
	entityURL entityKind = iota
	entityMention
	entityHashtag
)

type textEntity struct {
	start int
	end   int
	kind  entityKind
	value string
}

var (
	statusURLRegex      = regexp.MustCompile(`(?i)\b(?:https?://|xmpp:)[^\s<]+`)
	statusMentionRegex  = regexp.MustCompile(`(?i)@[a-z0-9_]+(?:[.-]+[a-z0-9_]+)*(?:@[a-z0-9_]+(?:[.-]+[a-z0-9_]+)*)?`)
	statusHashtagRegex  = regexp.MustCompile(`(?i)[#\x{FF03}][\p{L}\p{N}_\x{00B7}\x{30FB}\x{200C}]+`)
	statusURLPrefixExpr = regexp.MustCompile(`(?i)^(https?://(?:www\.)?|xmpp:)`)
	statusParagraphsRegex = regexp.MustCompile(`\n{2,}`)
)

func linkifyStatusText(cfg *config.Config, text string, opts statusContentOptions) string {
	return linkifyTextWithOptions(cfg, text, opts, false, false)
}

func linkifyAccountFieldText(cfg *config.Config, text string, opts statusContentOptions) string {
	return linkifyTextWithOptions(cfg, text, opts, true, true)
}

func linkifyTextWithOptions(cfg *config.Config, text string, opts statusContentOptions, withRelMe, withDomains bool) string {
	entities := extractTextEntities(text)
	if len(entities) == 0 {
		return stdhtml.EscapeString(text)
	}

	mentionLookup, usernameDomains := buildMentionLookup(opts.author, opts.mentions, cfg)

	var b strings.Builder
	last := 0
	for _, entity := range entities {
		if entity.start > last {
			b.WriteString(stdhtml.EscapeString(text[last:entity.start]))
		}
		switch entity.kind {
		case entityURL:
			b.WriteString(shortenedLink(entity.value, withRelMe))
		case entityMention:
			b.WriteString(formatMention(entity.value, mentionLookup, usernameDomains, cfg, withDomains))
		case entityHashtag:
			b.WriteString(formatHashtag(cfg, entity.value))
		}
		last = entity.end
	}
	if last < len(text) {
		b.WriteString(stdhtml.EscapeString(text[last:]))
	}
	return b.String()
}

func extractTextEntities(text string) []textEntity {
	var entities []textEntity

	// Optimization: Pre-check for URL indicators.
	// The regex explicitly requires "://" or "xmpp:", both of which contain ":".
	// If the regex is ever updated to support schemeless URLs (e.g. "www.example.com"),
	// this check must be updated or removed.
	if strings.Contains(text, ":") {
		for _, match := range statusURLRegex.FindAllStringIndex(text, -1) {
			start, end := match[0], match[1]
			end, urlValue := trimURLSuffix(text, start, end)
			if urlValue == "" {
				continue
			}
			entities = append(entities, textEntity{
				start: start,
				end:   end,
				kind:  entityURL,
				value: urlValue,
			})
		}
	}

	if strings.Contains(text, "@") {
		for _, match := range statusMentionRegex.FindAllStringIndex(text, -1) {
			start, end := match[0], match[1]
			if !validMentionBoundary(text, start) {
				continue
			}
			entities = append(entities, textEntity{
				start: start,
				end:   end,
				kind:  entityMention,
				value: text[start+1 : end],
			})
		}
	}

	if strings.Contains(text, "#") || strings.Contains(text, "\uff03") {
		for _, match := range statusHashtagRegex.FindAllStringIndex(text, -1) {
			start, end := match[0], match[1]
			if !validHashtagBoundary(text, start) {
				continue
			}
			tag := extractHashtag(text[start:end])
			if tag == "" {
				continue
			}
			entities = append(entities, textEntity{
				start: start,
				end:   end,
				kind:  entityHashtag,
				value: tag,
			})
		}
	}

	sort.Slice(entities, func(i, j int) bool {
		if entities[i].start != entities[j].start {
			return entities[i].start < entities[j].start
		}
		if entities[i].kind != entities[j].kind {
			return entities[i].kind < entities[j].kind
		}
		return entities[i].end > entities[j].end
	})

	filtered := make([]textEntity, 0, len(entities))
	cursor := 0
	for _, entity := range entities {
		if entity.start < cursor {
			continue
		}
		filtered = append(filtered, entity)
		cursor = entity.end
	}
	return filtered
}

func trimURLSuffix(text string, start, end int) (int, string) {
	trimmedEnd := end
	for trimmedEnd > start {
		r, size := utf8.DecodeLastRuneInString(text[:trimmedEnd])
		if !isTrailingURLPunct(r) {
			break
		}
		trimmedEnd -= size
	}
	if trimmedEnd <= start {
		return start, ""
	}
	return trimmedEnd, text[start:trimmedEnd]
}

func isTrailingURLPunct(r rune) bool {
	switch r {
	case '.', ',', ':', ';', '!', '?', ')', ']':
		return true
	default:
		return false
	}
}

func validMentionBoundary(text string, start int) bool {
	if start == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:start])
	if r == '/' || r == '=' {
		return false
	}
	return !isWordRune(r)
}

func validHashtagBoundary(text string, start int) bool {
	if start == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(text[:start])
	if r == '/' || r == '=' || r == ')' {
		return false
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func extractHashtag(value string) string {
	if value == "" {
		return ""
	}
	_, size := utf8.DecodeRuneInString(value)
	if size == 0 || size >= len(value) {
		return ""
	}
	tag := value[size:]
	hasLetter := false
	for _, r := range tag {
		if unicode.IsLetter(r) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return ""
	}
	return tag
}

type mentionAccount struct {
	Username string
	Acct     string
	URL      string
	Domain   string
}

func buildMentionLookup(author *Account, mentions []Mention, cfg *config.Config) (map[string]mentionAccount, map[string]map[string]struct{}) {
	lookup := make(map[string]mentionAccount)
	usernameDomains := make(map[string]map[string]struct{})

	addAccount := func(acc mentionAccount) {
		key := statusMentionKey(acc.Username, acc.Domain)
		if _, ok := lookup[key]; !ok {
			lookup[key] = acc
		}
		userKey := strings.ToLower(acc.Username)
		if _, ok := usernameDomains[userKey]; !ok {
			usernameDomains[userKey] = make(map[string]struct{})
		}
		usernameDomains[userKey][acc.Domain] = struct{}{}
	}

	if author != nil {
		username, domain := splitAcct(author.Acct, author.Username)
		addAccount(mentionAccount{
			Username: username,
			Acct:     author.Acct,
			URL:      author.URL,
			Domain:   normalizeDomain(domain),
		})
	}

	for _, mention := range mentions {
		username, domain := splitAcct(mention.Acct, mention.Username)
		addAccount(mentionAccount{
			Username: username,
			Acct:     mention.Acct,
			URL:      mention.URL,
			Domain:   normalizeDomain(domain),
		})
	}

	return lookup, usernameDomains
}

func statusMentionKey(username, domain string) string {
	return strings.ToLower(username) + "@" + normalizeDomain(domain)
}

func splitAcct(acct, fallback string) (string, string) {
	value := strings.TrimSpace(acct)
	if value == "" {
		return fallback, ""
	}
	parts := strings.SplitN(value, "@", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return value, ""
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	if domain == "" {
		return ""
	}
	if unicodeDomain, err := idna.Lookup.ToUnicode(domain); err == nil && unicodeDomain != "" {
		domain = unicodeDomain
	}
	return strings.ToLower(domain)
}

func formatMention(screenName string, lookup map[string]mentionAccount, usernameDomains map[string]map[string]struct{}, cfg *config.Config, withDomains bool) string {
	username, domain := splitAcct(screenName, screenName)
	domain = normalizeDomain(domain)
	if domain != "" && isLocalMentionDomain(domain, cfg) {
		domain = ""
	}

	account, ok := lookup[statusMentionKey(username, domain)]
	if !ok {
		return stdhtml.EscapeString("@" + screenName)
	}

	display := account.Username
	if withDomains && strings.TrimSpace(account.Acct) != "" {
		display = account.Acct
	} else {
		domains := usernameDomains[strings.ToLower(account.Username)]
		if len(domains) > 1 && strings.TrimSpace(account.Acct) != "" {
			display = account.Acct
		}
	}

	urlValue := strings.TrimSpace(account.URL)
	if urlValue == "" {
		return stdhtml.EscapeString("@" + display)
	}

	return `<span class="h-card" translate="no"><a href="` + stdhtml.EscapeString(urlValue) + `" class="u-url mention">@<span>` + stdhtml.EscapeString(display) + `</span></a></span>`
}

func isLocalMentionDomain(domain string, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	localDomain := normalizeDomain(cfg.Instance.LocalDomain)
	if localDomain != "" && strings.EqualFold(domain, localDomain) {
		return true
	}
	webDomain := normalizeDomain(cfg.Instance.WebDomain)
	return webDomain != "" && strings.EqualFold(domain, webDomain)
}

func formatHashtag(cfg *config.Config, tag string) string {
	urlValue := statusTagURL(cfg, tag)
	if urlValue == "" {
		return stdhtml.EscapeString("#" + tag)
	}
	return `<a href="` + stdhtml.EscapeString(urlValue) + `" class="mention hashtag" rel="tag">#<span>` + stdhtml.EscapeString(tag) + `</span></a>`
}

func statusTagURL(cfg *config.Config, tag string) string {
	domain := strings.TrimSpace(instancePublicDomain(cfg))
	escaped := url.PathEscape(tag)
	if domain == "" {
		return "/tags/" + escaped
	}
	return "https://" + domain + "/tags/" + escaped
}

func shortenedLink(rawURL string, relMe bool) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	if _, err := url.Parse(rawURL); err != nil {
		return stdhtml.EscapeString(rawURL)
	}

	prefix := statusURLPrefixExpr.FindString(rawURL)
	rest := rawURL[len(prefix):]
	restRunes := []rune(rest)
	cutoff := len(restRunes) > 30
	displayLen := len(restRunes)
	if displayLen > 30 {
		displayLen = 30
	}
	display := string(restRunes[:displayLen])
	suffix := string(restRunes[displayLen:])
	if cutoff && len([]rune(suffix)) == 1 {
		display += suffix
		suffix = ""
		cutoff = false
	}

	rel := "nofollow noopener"
	if relMe {
		rel += " me"
	}

	spanClass := ""
	if cutoff {
		spanClass = "ellipsis"
	}

	return `<a href="` + stdhtml.EscapeString(rawURL) + `" target="_blank" rel="` + rel + `" translate="no">` +
		`<span class="invisible">` + stdhtml.EscapeString(prefix) + `</span>` +
		`<span class="` + spanClass + `">` + stdhtml.EscapeString(display) + `</span>` +
		`<span class="invisible">` + stdhtml.EscapeString(suffix) + `</span>` +
		`</a>`
}

func simpleFormatHTML(html string) string {
	if html == "" {
		return ""
	}
	html = strings.ReplaceAll(html, "\r\n", "\n")
	html = strings.ReplaceAll(html, "\r", "\n")

	paragraphs := statusParagraphsRegex.Split(html, -1)
	for i, p := range paragraphs {
		p = strings.ReplaceAll(p, "\n", "<br />")
		paragraphs[i] = "<p>" + p + "</p>"
	}
	return strings.Join(paragraphs, "")
}

func addQuoteFallback(html string, quote *Status) string {
	if quote == nil {
		return html
	}
	quoteURL := ""
	if quote.URL != nil && strings.TrimSpace(*quote.URL) != "" {
		quoteURL = strings.TrimSpace(*quote.URL)
	} else if strings.TrimSpace(quote.URI) != "" {
		quoteURL = strings.TrimSpace(quote.URI)
	}
	if quoteURL == "" || strings.Contains(html, quoteURL) {
		return html
	}
	fallback := `<p class="quote-inline">RE: ` + shortenedLink(quoteURL, false) + `</p>`
	return fallback + html
}

var (
	mastodonPolicyOnce sync.Once
	mastodonPolicy     *bluemonday.Policy
)

func sanitizeStatusHTML(input string) string {
	if strings.TrimSpace(input) == "" {
		return ""
	}
	policy := mastodonStrictPolicy()
	sanitized := policy.Sanitize(input)
	return postProcessSanitizedHTML(sanitized)
}

func mastodonStrictPolicy() *bluemonday.Policy {
	mastodonPolicyOnce.Do(func() {
		policy := bluemonday.NewPolicy()
		policy.AllowElements("p", "br", "span", "a", "del", "s", "pre", "blockquote", "code", "b", "strong", "u", "i", "em", "ul", "ol", "li", "ruby", "rt", "rp")
		policy.AllowAttrs("href", "rel", "class", "translate").OnElements("a")
		policy.AllowAttrs("class", "translate").OnElements("span")
		policy.AllowAttrs("start", "reversed").OnElements("ol")
		policy.AllowAttrs("value").OnElements("li")
		policy.AllowAttrs("class").OnElements("p")
		policy.AllowURLSchemes("http", "https", "dat", "dweb", "ipfs", "ipns", "ssb", "gopher", "xmpp", "magnet", "gemini")
		policy.AllowRelativeURLs(false)
		policy.RequireParseableURLs(true)
		mastodonPolicy = policy
	})
	return mastodonPolicy
}

func postProcessSanitizedHTML(input string) string {
	if input == "" {
		return ""
	}
	doc, err := xhtml.Parse(strings.NewReader(input))
	if err != nil {
		return input
	}
	body := findBodyNode(doc)
	if body == nil {
		body = doc
	}

	walkAndSanitize(body)

	var buf bytes.Buffer
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		_ = xhtml.Render(&buf, child)
	}
	return buf.String()
}

func walkAndSanitize(node *xhtml.Node) {
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == xhtml.ElementNode {
			if child.Data == "a" && !sanitizeAnchor(child, node) {
				child = next
				continue
			}
			filterClassAttr(child)
			filterTranslateAttr(child)
		}
		walkAndSanitize(child)
		child = next
	}
}

func findBodyNode(node *xhtml.Node) *xhtml.Node {
	var body *xhtml.Node
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "body" {
			body = n
			return
		}
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return body
}

func sanitizeAnchor(node, parent *xhtml.Node) bool {
	href := attrValue(node, "href")
	if href == "" || !isAllowedLinkScheme(href) {
		if parent != nil {
			textNode := &xhtml.Node{Type: xhtml.TextNode, Data: nodeText(node)}
			parent.InsertBefore(textNode, node)
			parent.RemoveChild(node)
		} else {
			node.Type = xhtml.TextNode
			node.Data = nodeText(node)
			node.Attr = nil
			node.FirstChild = nil
		}
		return false
	}
	setAttr(node, "rel", "nofollow noopener")
	setAttr(node, "target", "_blank")
	return true
}

func isAllowedLinkScheme(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme == "" {
		return false
	}
	switch scheme {
	case "http", "https", "dat", "dweb", "ipfs", "ipns", "ssb", "gopher", "xmpp", "magnet", "gemini":
		return true
	default:
		return false
	}
}

func filterClassAttr(node *xhtml.Node) {
	classValue := attrValue(node, "class")
	if classValue == "" {
		return
	}
	parts := strings.Fields(classValue)
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if allowedClass(part) {
			filtered = append(filtered, part)
		}
	}
	if len(filtered) == 0 {
		removeAttr(node, "class")
		return
	}
	setAttr(node, "class", strings.Join(filtered, " "))
}

func allowedClass(value string) bool {
	if strings.HasPrefix(value, "h-") || strings.HasPrefix(value, "p-") || strings.HasPrefix(value, "u-") ||
		strings.HasPrefix(value, "dt-") || strings.HasPrefix(value, "e-") {
		return true
	}
	switch value {
	case "mention", "hashtag", "ellipsis", "invisible", "quote-inline":
		return true
	default:
		return false
	}
}

func filterTranslateAttr(node *xhtml.Node) {
	translateValue := attrValue(node, "translate")
	if translateValue == "" {
		return
	}
	if translateValue != "no" {
		removeAttr(node, "translate")
	}
}

func nodeText(node *xhtml.Node) string {
	var buf strings.Builder
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			buf.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(node)
	return buf.String()
}

func attrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func setAttr(node *xhtml.Node, key, value string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr[i].Val = value
			return
		}
	}
	node.Attr = append(node.Attr, xhtml.Attribute{Key: key, Val: value})
}

func removeAttr(node *xhtml.Node, key string) {
	for i := range node.Attr {
		if node.Attr[i].Key == key {
			node.Attr = append(node.Attr[:i], node.Attr[i+1:]...)
			return
		}
	}
}
