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
	"unpaid":    "Overdue",
	"paid":      "Paid",
	"overdue":   "Overdue",
	"draft":     "Draft",
	"pending":   "Pending",
	"submitted": "Submitted",
	"cancelled": "Cancelled",
	"completed": "Completed",
	"open":      "Open",
	"closed":    "Closed",
	"active":    "Active",
}

// ParseAmount strictly parses strings like "10k", "5m", "1cr" or raw integers.
func ParseAmount(token string) (int, bool) {
	token = strings.ReplaceAll(token, ",", "")
	
	if strings.HasSuffix(token, "k") {
		val, err := strconv.ParseFloat(strings.TrimSuffix(token, "k"), 64)
		return int(val * 1000), err == nil
	}
	if strings.HasSuffix(token, "l") || strings.HasSuffix(token, "lakh") || strings.HasSuffix(token, "lakhs") {
		clean := strings.TrimRight(token, "lakhs") // Trims all characters 'l', 'a', 'k', 'h', 's'
		if clean == "" { clean = "1" } // edge case for just "lakh"
        // Wait, TrimRight removes any characters from the set. Better to use ReplaceAll or precise stripping.
        // Actually this is just a quick regex or precise strings.HasSuffix.
	}
	// Let's implement robust multiplier matching
	multipliers := map[string]float64{
		"k": 1e3, "l": 1e5, "lakh": 1e5, "lakhs": 1e5,
		"m": 1e6, "million": 1e6, "millions": 1e6,
		"cr": 1e7, "crore": 1e7, "crores": 1e7,
	}
	
	for suffix, mult := range multipliers {
		if strings.HasSuffix(token, suffix) {
			prefix := strings.TrimSuffix(token, suffix)
			if prefix == "" {
				prefix = "1"
			}
			val, err := strconv.ParseFloat(prefix, 64)
			if err == nil {
				return int(val * mult), true
			}
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

// ParseDateRange detects concepts like "last month", "this year", "today".
func ParseDateRange(tokens []string) *Filter {
	now := time.Now()

	for i, t := range tokens {
		var start, end time.Time
		matched := false

		if t == "today" {
			start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			end = start.AddDate(0, 0, 1).Add(-time.Nanosecond)
			matched = true
		} else if t == "yesterday" {
			start = time.Date(now.Year(), now.Month(), now.Day()-1, 0, 0, 0, 0, now.Location())
			end = start.AddDate(0, 0, 1).Add(-time.Nanosecond)
			matched = true
		} else if t == "this" && i+1 < len(tokens) {
			if tokens[i+1] == "week" {
				// Naive "this week" (assuming week starts Sunday or Monday depending on math, keeping it simple: last 7 days)
				start = time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday()), 0, 0, 0, 0, now.Location())
				end = start.AddDate(0, 0, 7).Add(-time.Nanosecond)
				matched = true
			} else if tokens[i+1] == "month" {
				start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
				end = start.AddDate(0, 1, 0).Add(-time.Nanosecond)
				matched = true
			} else if tokens[i+1] == "year" {
				start = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
				end = start.AddDate(1, 0, 0).Add(-time.Nanosecond)
				matched = true
			}
		} else if t == "last" && i+1 < len(tokens) {
			if tokens[i+1] == "week" {
				start = time.Date(now.Year(), now.Month(), now.Day()-int(now.Weekday())-7, 0, 0, 0, 0, now.Location())
				end = start.AddDate(0, 0, 7).Add(-time.Nanosecond)
				matched = true
			} else if tokens[i+1] == "month" {
				start = time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, now.Location())
				end = start.AddDate(0, 1, 0).Add(-time.Nanosecond)
				matched = true
			} else if tokens[i+1] == "year" {
				start = time.Date(now.Year()-1, 1, 1, 0, 0, 0, 0, now.Location())
				end = start.AddDate(1, 0, 0).Add(-time.Nanosecond)
				matched = true
			}
		}

		if matched {
			return &Filter{
				Field: "posting_date", // Default assumption
				Op:    "between",
				Value: []time.Time{start, end},
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
