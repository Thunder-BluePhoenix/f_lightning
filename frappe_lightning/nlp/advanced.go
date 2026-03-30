package nlp

import (
	"regexp"
	"strings"
)

// AdvancedDSLRegex matches patterns like field:"value" or field:>100
var AdvancedDSLRegex = regexp.MustCompile(`(\w+):(?:([<>=!]+)?)([^\s"]+|"[^"]+")`)

// ExtractAdvancedDSL identifies explicit power-user filters (e.g., status:"Paid" amount:>5000)
// It removes them from the raw query and returns the structured Filters.
func ExtractAdvancedDSL(query string) (string, []Filter) {
	var filters []Filter

	matches := AdvancedDSLRegex.FindAllStringSubmatch(query, -1)
	for _, m := range matches {
		if len(m) == 4 {
			field := m[1]
			op := m[2]
			if op == "" {
				op = "="
			}
			val := strings.Trim(m[3], `"`)

			filters = append(filters, Filter{
				Field: field,
				Op:    op,
				Value: val,
			})
		}
	}

	// Clean up the query
	cleanQuery := AdvancedDSLRegex.ReplaceAllString(query, "")
	cleanQuery = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(cleanQuery, " "))

	return cleanQuery, filters
}
