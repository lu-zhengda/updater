package app

// SourceOverride stores the resolved explicit source override payload that was
// applied during enrichment.
type SourceOverride struct {
	Kind       string
	Repo       string
	AppcastURL string
	Cask       string
}
