package nlp

// DocTypeMap acts as a dictionary mapping synonyms and plural forms to Frappe DocTypes.
var DocTypeMap = map[string]string{
	"invoice":   "Sales Invoice",
	"invoices":  "Sales Invoice",
	"customer":  "Customer",
	"customers": "Customer",
	"item":      "Item",
	"items":     "Item",
	"quote":     "Quotation",
	"quotation": "Quotation",
	"supplier":  "Supplier",
	"suppliers": "Supplier",
}

// DetectDocType analyzes the token stream to identify the primary target DocType.
func DetectDocType(tokens []string) string {
	for _, t := range tokens {
		if dt, ok := DocTypeMap[t]; ok {
			return dt
		}
	}
	return ""
}
