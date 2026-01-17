package v1

import (
	"bytes"
	stdhtml "html"
	"net/url"
	"regexp"
	"slices"
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
	statusURLRegex     = regexp.MustCompile(`(?i)\b(?:https?://|xmpp:)[^\s<]+`)
	statusMentionRegex = regexp.MustCompile(`(?i)@[a-z0-9_]+(?:[.-]+[a-z0-9_]+)*(?:@[a-z0-9_]+(?:[.-]+[a-z0-9_]+)*)?`)
	statusHashtagRegex = regexp.MustCompile(`(?i)[#\x{FF03}][\p{L}\p{N}_\x{00B7}\x{30FB}\x{200C}]+`)
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

	hasMentions := false
	for _, e := range entities {
		if e.kind == entityMention {
			hasMentions = true
			break
		}
	}

	var mentionLookup map[string]mentionAccount
	var usernameDomains map[string]map[string]struct{}
	// Optimization: Skip expensive map build if no mentions are present
	if hasMentions {
		mentionLookup, usernameDomains = buildMentionLookup(opts.author, opts.mentions, cfg)
	}

	var b strings.Builder
	// Conservative estimate: length of text + 20% overhead + 40 chars per entity for HTML tags
	b.Grow(len(text) + len(text)/5 + len(entities)*40)

	last := 0
	for _, entity := range entities {
		if entity.start > last {
			writeEscapeString(&b, text[last:entity.start])
		}
		switch entity.kind {
		case entityURL:
			writeShortenedLink(&b, entity.value, withRelMe)
		case entityMention:
			writeMention(&b, entity.value, mentionLookup, usernameDomains, cfg, withDomains)
		case entityHashtag:
			writeHashtag(&b, cfg, entity.value)
		}
		last = entity.end
	}
	if last < len(text) {
		writeEscapeString(&b, text[last:])
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

	if len(entities) > 1 {
		slices.SortFunc(entities, func(a, b textEntity) int {
			if a.start != b.start {
				return a.start - b.start
			}
			if a.kind != b.kind {
				return int(a.kind - b.kind)
			}
			return b.end - a.end
		})
	}

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
	var b strings.Builder
	writeMention(&b, screenName, lookup, usernameDomains, cfg, withDomains)
	return b.String()
}

func writeMention(b *strings.Builder, screenName string, lookup map[string]mentionAccount, usernameDomains map[string]map[string]struct{}, cfg *config.Config, withDomains bool) {
	username, domain := splitAcct(screenName, screenName)
	domain = normalizeDomain(domain)
	if domain != "" && isLocalMentionDomain(domain, cfg) {
		domain = ""
	}

	account, ok := lookup[statusMentionKey(username, domain)]
	if !ok {
		b.WriteString("@")
		writeEscapeString(b, screenName)
		return
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
		b.WriteString("@")
		writeEscapeString(b, display)
		return
	}

	b.WriteString(`<span class="h-card" translate="no"><a href="`)
	writeEscapeString(b, urlValue)
	b.WriteString(`" class="u-url mention">@<span>`)
	writeEscapeString(b, display)
	b.WriteString(`</span></a></span>`)
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
	var b strings.Builder
	writeHashtag(&b, cfg, tag)
	return b.String()
}

func writeHashtag(b *strings.Builder, cfg *config.Config, tag string) {
	urlValue := statusTagURL(cfg, tag)
	if urlValue == "" {
		b.WriteString("#")
		writeEscapeString(b, tag)
		return
	}
	b.WriteString(`<a href="`)
	writeEscapeString(b, urlValue)
	b.WriteString(`" class="mention hashtag" rel="tag">#<span>`)
	writeEscapeString(b, tag)
	b.WriteString(`</span></a>`)
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
	var b strings.Builder
	writeShortenedLink(&b, rawURL, relMe)
	return b.String()
}

func findURLPrefix(s string) string {
	if len(s) >= 8 && strings.EqualFold(s[:8], "https://") {
		if len(s) >= 12 && strings.EqualFold(s[:12], "https://www.") {
			return s[:12]
		}
		return s[:8]
	}
	if len(s) >= 7 && strings.EqualFold(s[:7], "http://") {
		if len(s) >= 11 && strings.EqualFold(s[:11], "http://www.") {
			return s[:11]
		}
		return s[:7]
	}
	if len(s) >= 5 && strings.EqualFold(s[:5], "xmpp:") {
		return s[:5]
	}
	return ""
}

func writeShortenedLink(b *strings.Builder, rawURL string, relMe bool) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return
	}
	// Note: We skip url.Parse check here for performance.
	// The caller should ensure the URL is reasonably valid (e.g. from regex).

	prefix := findURLPrefix(rawURL)
	rest := rawURL[len(prefix):]

	// Find byte index for 30th rune and check for cutoff
	displayByteLen := len(rest)
	cutoff := false

	// Iterate up to 30 runes to find truncation point
	runeCount := 0
	idx := 0
	for i := 0; i < 30 && idx < len(rest); i++ {
		_, size := utf8.DecodeRuneInString(rest[idx:])
		idx += size
		runeCount++
	}

	// If we stopped before end of string, check remaining
	if idx < len(rest) {
		// Check if exactly 1 rune remains (edge case from original code)
		remaining := rest[idx:]
		_, size := utf8.DecodeRuneInString(remaining)
		if size < len(remaining) {
			// More than 1 rune remains, so cutoff
			cutoff = true
			displayByteLen = idx
		}
		// If exactly 1 rune remains, we don't cutoff and show it all
	}

	display := rest[:displayByteLen]
	suffix := ""
	if cutoff {
		suffix = rest[displayByteLen:]
	}

	rel := "nofollow noopener"
	if relMe {
		rel += " me"
	}

	spanClass := ""
	if cutoff {
		spanClass = "ellipsis"
	}

	b.WriteString(`<a href="`)
	writeEscapeString(b, rawURL)
	b.WriteString(`" target="_blank" rel="`)
	b.WriteString(rel)
	b.WriteString(`" translate="no">`)
	b.WriteString(`<span class="invisible">`)
	writeEscapeString(b, prefix)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="`)
	b.WriteString(spanClass)
	b.WriteString(`">`)
	writeEscapeString(b, display)
	b.WriteString(`</span>`)
	b.WriteString(`<span class="invisible">`)
	writeEscapeString(b, suffix)
	b.WriteString(`</span>`)
	b.WriteString(`</a>`)
}

// writeEscapeString escapes special characters like html.EscapeString but writes to strings.Builder
// to avoid intermediate allocations.
func writeEscapeString(b *strings.Builder, s string) {
	start := 0
	for i := 0; i < len(s); i++ {
		var escaped string
		switch s[i] {
		case '"':
			escaped = "&#34;"
		case '&':
			escaped = "&amp;"
		case '\'':
			escaped = "&#39;"
		case '<':
			escaped = "&lt;"
		case '>':
			escaped = "&gt;"
		default:
			continue
		}
		b.WriteString(s[start:i])
		b.WriteString(escaped)
		start = i + 1
	}
	b.WriteString(s[start:])
}

func simpleFormatHTML(html string) string {
	if html == "" {
		return ""
	}

	var b strings.Builder
	// Estimate growth: original length + roughly 5% overhead for tags
	b.Grow(len(html) + len(html)/20 + 16)

	b.WriteString("<p>")

	n := len(html)
	start := 0
	i := 0

	for i < n {
		c := html[i]
		if c == '\r' || c == '\n' {
			if i > start {
				b.WriteString(html[start:i])
			}

			newlines := 0
			j := i
			for j < n {
				c2 := html[j]
				if c2 == '\r' {
					newlines++
					j++
					if j < n && html[j] == '\n' {
						j++
					}
				} else if c2 == '\n' {
					newlines++
					j++
				} else {
					break
				}
			}

			if newlines >= 2 {
				b.WriteString("</p><p>")
			} else {
				b.WriteString("<br />")
			}

			start = j
			i = j
		} else {
			i++
		}
	}

	if start < n {
		b.WriteString(html[start:])
	}

	b.WriteString("</p>")

	return b.String()
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
	// Optimization: extracting scheme manually is significantly faster than url.Parse
	// and avoids allocations.
	idx := strings.IndexByte(rawURL, ':')
	if idx == -1 {
		return false
	}
	scheme := rawURL[:idx]
	if len(scheme) == 0 || len(scheme) > 20 {
		return false
	}
	scheme = strings.ToLower(scheme)
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
