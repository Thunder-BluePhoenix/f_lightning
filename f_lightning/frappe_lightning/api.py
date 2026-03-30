import frappe
from frappe import _

@frappe.whitelist()
def get_analytics_data(days=30):
    """
    Returns aggregated metrics for the Lightning Search Analytics dashboard.
    """
    if not frappe.has_permission("Lightning Search Log", "read"):
        frappe.throw(_("Not permitted"), frappe.PermissionError)

    # Calculate date boundary
    since_date = frappe.utils.add_days(frappe.utils.today(), -int(days))

    # 1. Total Volume & Latency
    metrics = frappe.db.sql("""
        SELECT 
            COUNT(*) as total_searches,
            AVG(latency_ms) as avg_latency
        FROM `tabLightning Search Log`
        WHERE creation >= %s
    """, (since_date,), as_dict=True)[0]

    # 2. Zero Results Queries
    zero_results = frappe.db.sql("""
        SELECT query, COUNT(*) as count 
        FROM `tabLightning Search Log`
        WHERE hits = 0 AND creation >= %s
        GROUP BY query
        ORDER BY count DESC
        LIMIT 10
    """, (since_date,), as_dict=True)

    # 3. Top Clicked Targets
    top_targets = frappe.db.sql("""
        SELECT clicked_doctype, clicked_name, COUNT(*) as count
        FROM `tabLightning Search Log`
        WHERE clicked_doctype IS NOT NULL AND creation >= %s
        GROUP BY clicked_doctype, clicked_name
        ORDER BY count DESC
        LIMIT 10
    """, (since_date,), as_dict=True)

    # 4. Latency Distribution
    # Simply grouping to 0-10, 10-50, 50-100, 100+ buckets
    latency_buckets = frappe.db.sql("""
        SELECT 
            SUM(CASE WHEN latency_ms <= 10 THEN 1 ELSE 0 END) as 'sub_10',
            SUM(CASE WHEN latency_ms > 10 AND latency_ms <= 50 THEN 1 ELSE 0 END) as 'sub_50',
            SUM(CASE WHEN latency_ms > 50 AND latency_ms <= 100 THEN 1 ELSE 0 END) as 'sub_100',
            SUM(CASE WHEN latency_ms > 100 THEN 1 ELSE 0 END) as 'over_100'
        FROM `tabLightning Search Log`
        WHERE creation >= %s
    """, (since_date,), as_dict=True)[0]

    return {
        "metrics": metrics,
        "zero_results": zero_results,
        "top_targets": top_targets,
        "latency": latency_buckets
    }

@frappe.whitelist(allow_guest=True)
def log_search():
    """Fallback endpoint in case the local JS analytics ping fails, or if we want to store it securely from backend."""
    # We will log natively via the Go Proxy, but this allows direct DB writes via frail network fallback.
    pass
