package nlp

import (
	"strings"
)

// Tokenize converts a natural language string into a clean slice of lowercased tokens.
// It removes commas and standardizes spacing.
func Tokenize(input string) []string {
	input = strings.ToLower(input)
	// Remove basic punctuation that could interfere with parsing
	replacements := []string{",", "", ".", "", "?", "", "!", ""}
	r := strings.NewReplacer(replacements...)
	input = r.Replace(input)

	return strings.Fields(input)
}
