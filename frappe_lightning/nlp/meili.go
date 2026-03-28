package nlp

import (
	"fmt"
	"time"
)

// ToMeili translates the DSL Query struct into an Meilisearch compatible parameters map.
func ToMeili(q Query) map[string]interface{} {
	var filters []string

	// Enforce DocType filtering securely
	if q.DocType != "" {
		filters = append(filters, fmt.Sprintf("doctype = '%s'", q.DocType))
	}

	for _, f := range q.Filters {
		switch f.Op {
		case "=":
			filters = append(filters, fmt.Sprintf("%s = '%v'", f.Field, f.Value))
		case ">":
			filters = append(filters, fmt.Sprintf("%s > %v", f.Field, f.Value))
		case "<":
			filters = append(filters, fmt.Sprintf("%s < %v", f.Field, f.Value))
		case "between":
			if dates, ok := f.Value.([]time.Time); ok && len(dates) == 2 {
				// Meilisearch works well with UNIX timestamps for ranges
				filters = append(filters, fmt.Sprintf("%s >= %d AND %s <= %d", 
					f.Field, dates[0].Unix(), 
					f.Field, dates[1].Unix()))
			}
		}
	}

	return map[string]interface{}{
		"q":      "", // Typically Meili 'q' is the unstructured search string, can use q.Text if we want partial matches
		"filter": filters, // Array of strings is implicitly AND'ed in Meilisearch
		"limit":  q.Limit,
	}
}
