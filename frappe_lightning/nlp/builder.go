package nlp

// Rules registry allows for easy extensibility of the parsing engine.
// Developers can hook into this pipeline to add custom DocType-specific rules.
var Rules = []Rule{
	DetectAmountFilter,
	ParseDateRange,
	DetectStatusFilter,
}

// BuildQuery parses a natural language string and applies all registered rules
// to transform messy user input into a deterministic structured Query.
func BuildQuery(input string) Query {
	cleanInput, extFilters := ExtractAdvancedDSL(input)
	tokens := Tokenize(cleanInput)

	q := Query{
		DocType: DetectDocType(tokens),
		Limit:   20, // Default limit
		Filters: extFilters,
		Text:    cleanInput,
	}

	// Apply all rules sequentially out-of-the-box
	for _, rule := range Rules {
		if f := rule(tokens); f != nil {
			q.Filters = append(q.Filters, *f)
		}
	}

	return q
}
