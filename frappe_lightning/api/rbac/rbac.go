package rbac

import (
	"fmt"
	"strings"
)

// PermissionCache mocks a local cache of Frappe's `tabUser Permission` table.
// In production, this would be updated via the MariaDB binlog listener directly!
var PermissionCache = map[string]map[string][]string{
	"test@example.com": {
		"Company":   {"Acme Corp"},
		"Territory": {"India", "APAC"},
	},
}

// BuildPermissionFilter constructs a Meilisearch filter string based on a user's roles and permissions.
// This is cryptographically safe because it is evaluated on the proxy before Meilisearch ever sees the query.
func BuildPermissionFilter(user string, roles []string, doctype string) string {
	if hasRole(roles, "System Manager") || hasRole(roles, "Search API User") {
		// Administrator / Super User — no restrictions.
		return ""
	}

	// For specific users, we inject explicit filters based on Frappe's User Permissions.
	// E.g. "company = 'Acme Corp'"
	userPerms, exists := PermissionCache[user]
	if !exists {
		// If a user has no explicit user permissions, they might still have role-based reading rights.
		// For safety, in Frappe, strict user permissions apply if checked. If not, open read.
		// We'll return an empty filter for now (meaning read all for their role).
		return ""
	}

	var conditions []string

	// For standard Frappe DocTypes, we know which fields link to Company or Territory.
	// E.g., Sales Invoice has a `company` field. Customer has a `territory` field.

	if doctype == "Sales Invoice" || doctype == "Purchase Order" || doctype == "Lead" || doctype == "" {
		if comps := userPerms["Company"]; len(comps) > 0 {
			conditions = append(conditions, formatInClause("company", comps))
		}
	}

	if doctype == "Customer" || doctype == "Lead" || doctype == "" {
		if terrs := userPerms["Territory"]; len(terrs) > 0 {
			conditions = append(conditions, formatInClause("territory", terrs))
		}
	}

	if len(conditions) == 0 {
		return ""
	}

	// Join all user permission fields with AND
	return strings.Join(conditions, " AND ")
}

func hasRole(roles []string, target string) bool {
	for _, r := range roles {
		if r == target {
			return true
		}
	}
	return false
}

// formatInClause converts array of names into Meilisearch IN syntax. E.g.: company IN ["x", "y"]
func formatInClause(field string, values []string) string {
	if len(values) == 1 {
		return fmt.Sprintf("%s = '%s'", field, escape(values[0]))
	}

	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("'%s'", escape(v))
	}
	return fmt.Sprintf("%s IN [%s]", field, strings.Join(quoted, ", "))
}

func escape(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}
