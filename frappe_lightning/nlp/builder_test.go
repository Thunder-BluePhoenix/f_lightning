package nlp

import (
	"testing"
)

func TestBuildQuery(t *testing.T) {
	input := "unpaid invoices last month above 10k"
	q := BuildQuery(input)

	if q.DocType != "Sales Invoice" {
		t.Errorf("Expected DocType 'Sales Invoice', got '%s'", q.DocType)
	}

	if len(q.Filters) != 3 {
		t.Fatalf("Expected 3 filters, got %d", len(q.Filters))
	}

	hasStatus := false
	hasAmount := false
	for _, f := range q.Filters {
		if f.Field == "status" && f.Value == "Overdue" {
			hasStatus = true
		}
		if f.Field == "grand_total" && f.Op == ">" && f.Value == 10000 {
			hasAmount = true
		}
	}

	if !hasStatus {
		t.Error("Expected status filter for 'Overdue'")
	}
	if !hasAmount {
		t.Error("Expected amount filter for '> 10000'")
	}
}
