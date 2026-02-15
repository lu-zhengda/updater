package tui

import (
	"regexp"
	"strings"
)

// Common HTML entity replacements.
var htmlEntities = map[string]string{
	"&amp;":  "&",
	"&lt;":   "<",
	"&gt;":   ">",
	"&quot;": `"`,
	"&apos;": "'",
	"&#39;":  "'",
	"&nbsp;": " ",
}

var (
	reBlockTags = regexp.MustCompile(`(?i)<(?:br|/p|/div|/li|/tr)\s*/?>`)
	reListItem  = regexp.MustCompile(`(?i)<li\s*[^>]*>`)
	reAllTags   = regexp.MustCompile(`<[^>]+>`)
	reEntity    = regexp.MustCompile(`&[#\w]+;`)
	reMultiNL   = regexp.MustCompile(`\n{3,}`)
)

// StripHTML converts HTML content to plain text suitable for terminal display.
// It converts block-level elements to newlines, strips all tags, and decodes entities.
func StripHTML(s string) string {
	// Convert block-level closing tags to newlines.
	s = reBlockTags.ReplaceAllString(s, "\n")

	// Convert list items to bullet points.
	s = reListItem.ReplaceAllString(s, "\n• ")

	// Strip all remaining HTML tags.
	s = reAllTags.ReplaceAllString(s, "")

	// Decode HTML entities.
	s = reEntity.ReplaceAllStringFunc(s, func(entity string) string {
		if r, ok := htmlEntities[strings.ToLower(entity)]; ok {
			return r
		}
		return entity
	})

	// Collapse excessive newlines.
	s = reMultiNL.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}
