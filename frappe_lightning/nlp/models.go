package nlp

// Query represents the internal structured DSL for a search.
type Query struct {
	DocType string   `json:"doctype"`
	Filters []Filter `json:"filters"`
	Text    string   `json:"text"`
	Sort    string   `json:"sort"`
	Limit   int      `json:"limit"`
}

// Filter represents a parsed condition to be applied to the search index.
type Filter struct {
	Field string      `json:"field"`
	Op    string      `json:"op"`
	Value interface{} `json:"value"`
}
