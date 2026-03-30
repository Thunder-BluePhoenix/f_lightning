package config

// IndexSchema defines the Meilisearch index settings for a single DocType.
type IndexSchema struct {
	Name        string   // Frappe DocType name, e.g. "Sales Invoice"
	Table       string   // MariaDB table name, e.g. "tabSales Invoice"
	IndexSuffix string   // e.g. "sales_invoice"  (used in {site}_{suffix})
	Fields      []string // Fields to extract from binlog rows
	Searchable  []string // Fields Meilisearch should search
	Filterable  []string // Fields available for filter expressions
	Sortable    []string // Fields available for sort expressions
}

// DefaultSchemas returns the built-in set of DocType schemas.
// These can be overridden or extended via schema.yaml in the future.
func DefaultSchemas() []IndexSchema {
	return []IndexSchema{
		{
			Name:        "Sales Invoice",
			Table:       "tabSales Invoice",
			IndexSuffix: "sales_invoice",
			Fields:      []string{"name", "customer", "customer_name", "status", "grand_total", "outstanding_amount", "posting_date", "due_date", "company", "currency", "docstatus", "owner"},
			Searchable:  []string{"name", "customer_name", "customer"},
			Filterable:  []string{"status", "company", "posting_date", "grand_total", "outstanding_amount", "currency", "doctype"},
			Sortable:    []string{"posting_date", "grand_total", "outstanding_amount"},
		},
		{
			Name:        "Customer",
			Table:       "tabCustomer",
			IndexSuffix: "customer",
			Fields:      []string{"name", "customer_name", "customer_group", "customer_type", "territory", "mobile_no", "email_id", "docstatus"},
			Searchable:  []string{"name", "customer_name", "mobile_no", "email_id"},
			Filterable:  []string{"customer_group", "customer_type", "territory", "doctype"},
			Sortable:    []string{"customer_name"},
		},
		{
			Name:        "Item",
			Table:       "tabItem",
			IndexSuffix: "item",
			Fields:      []string{"name", "item_name", "item_code", "item_group", "description", "standard_rate", "stock_uom", "disabled", "docstatus"},
			Searchable:  []string{"name", "item_name", "item_code", "description"},
			Filterable:  []string{"item_group", "disabled", "stock_uom", "doctype"},
			Sortable:    []string{"item_name", "standard_rate"},
		},
		{
			Name:        "Purchase Order",
			Table:       "tabPurchase Order",
			IndexSuffix: "purchase_order",
			Fields:      []string{"name", "supplier", "supplier_name", "status", "grand_total", "transaction_date", "schedule_date", "company", "docstatus"},
			Searchable:  []string{"name", "supplier_name", "supplier"},
			Filterable:  []string{"status", "company", "transaction_date", "grand_total", "doctype"},
			Sortable:    []string{"transaction_date", "grand_total"},
		},
		{
			Name:        "Supplier",
			Table:       "tabSupplier",
			IndexSuffix: "supplier",
			Fields:      []string{"name", "supplier_name", "supplier_group", "supplier_type", "country", "mobile_no", "email_id", "docstatus"},
			Searchable:  []string{"name", "supplier_name", "mobile_no", "email_id"},
			Filterable:  []string{"supplier_group", "supplier_type", "country", "doctype"},
			Sortable:    []string{"supplier_name"},
		},
		{
			Name:        "Lead",
			Table:       "tabLead",
			IndexSuffix: "lead",
			Fields:      []string{"name", "lead_name", "company_name", "status", "email_id", "mobile_no", "lead_owner", "docstatus"},
			Searchable:  []string{"name", "lead_name", "company_name", "email_id"},
			Filterable:  []string{"status", "lead_owner", "doctype"},
			Sortable:    []string{"name"},
		},
	}
}

// SchemaByTable returns a schema looked up by MariaDB table name (fast O(n) scan — small list).
func SchemaByTable(schemas []IndexSchema, table string) (*IndexSchema, bool) {
	for i := range schemas {
		if schemas[i].Table == table {
			return &schemas[i], true
		}
	}
	return nil, false
}

// AllowedFields returns a set (map) of field names that should be included in the index document.
func (s *IndexSchema) AllowedFields() map[string]bool {
	m := make(map[string]bool, len(s.Fields))
	for _, f := range s.Fields {
		m[f] = true
	}
	return m
}

// IndexName returns the Meilisearch index name for a given site.
func (s *IndexSchema) IndexName(site string) string {
	return slugify(site) + "_" + s.IndexSuffix
}

// slugify converts a site name like "erp.local" to "erp_local" for use in index names.
func slugify(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '.' || c == '-' || c == '/' || c == ':' {
			out[i] = '_'
		} else {
			out[i] = c
		}
	}
	return string(out)
}
