package nlp

import (
	"strconv"
	"strings"
	"time"
)

// Rule represents a function that extracts a specific Filter from a token stream.
type Rule func([]string) *Filter

// StatusMap maps natural language adjectives to standard Frappe Status fields.
var StatusMap = map[string]string{
	"unpaid":  "Overdue",
	"paid":    "Paid",
	"overdue": "Overdue",
	"draft":   "Draft",
	"pending": "Pending",
}

// ParseAmount strictly parses strings like "10k", "5m", or raw integers.
func ParseAmount(token string) (int, bool) {
	if strings.HasSuffix(token, "k") {
		val, err := strconv.Atoi(strings.TrimSuffix(token, "k"))
		if err == nil {
			return val * 1000, true
		}
	}
	if strings.HasSuffix(token, "l") { // Lakhs
		val, err := strconv.Atoi(strings.TrimSuffix(token, "l"))
		if err == nil {
			return val * 100000, true
		}
	}
	
	val, err := strconv.Atoi(token)
	if err == nil {
		return val, true
	}
	return 0, false
}

// DetectAmountFilter looks for comparative terms ("above", "greater") followed by an amount.
func DetectAmountFilter(tokens []string) *Filter {
	for i, t := range tokens {
		if t == "above" || t == "greater" || t == ">" {
			if i+1 < len(tokens) {
				if val, ok := ParseAmount(tokens[i+1]); ok {
					return &Filter{
						Field: "grand_total", // Default assumption, can be contextual later
						Op:    ">",
						Value: val,
					}
				}
			}
		}
		if t == "below" || t == "less" || t == "under" || t == "<" {
			if i+1 < len(tokens) {
				if val, ok := ParseAmount(tokens[i+1]); ok {
					return &Filter{
						Field: "grand_total",
						Op:    "<",
						Value: val,
					}
				}
			}
		}
	}
	return nil
}

// ParseDateRange detects concepts like "last month" or "this year".
func ParseDateRange(tokens []string) *Filter {
	now := time.Now()
	for i := range tokens {
		if tokens[i] == "last" && i+1 < len(tokens) {
			if tokens[i+1] == "month" {
				start := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
				end := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).Add(-time.Nanosecond)
				return &Filter{
					Field: "posting_date", // Default date field
					Op:    "between",
					Value: []time.Time{start, end},
				}
			}
		}
	}
	return nil
}

// DetectStatusFilter checks tags against the predefined Status map.
func DetectStatusFilter(tokens []string) *Filter {
	for _, t := range tokens {
		if status, ok := StatusMap[t]; ok {
			return &Filter{
				Field: "status",
				Op:    "=",
				Value: status,
			}
		}
	}
	return nil
}
