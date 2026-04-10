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

@frappe.whitelist()
def get_webhook_dashboard_data():
	"""
	Returns data for the Lightning Webhook Dashboard page.
	Reads recent deliveries and subscription list from the Frappe DB.
	"""
	if not frappe.has_permission("Lightning Webhook", "read"):
		frappe.throw(_("Not permitted"), frappe.PermissionError)

	# 1. Subscription list with counts
	subscriptions = frappe.db.sql("""
		SELECT
			w.name,
			w.doctype_filter,
			w.endpoint_url,
			w.enabled,
			w.max_retries,
			COUNT(l.name)                                                    AS total_deliveries,
			SUM(CASE WHEN l.success = 1 THEN 1 ELSE 0 END)                  AS successful,
			SUM(CASE WHEN l.success = 0 THEN 1 ELSE 0 END)                  AS failed,
			ROUND(AVG(l.latency_ms), 0)                                      AS avg_latency_ms
		FROM `tabLightning Webhook` w
		LEFT JOIN `tabLightning Webhook Log` l ON l.subscription = w.name
		GROUP BY w.name
		ORDER BY w.enabled DESC, w.name ASC
	""", as_dict=True)

	# 2. Recent 50 delivery log entries
	recent_logs = frappe.db.sql("""
		SELECT
			name, delivery_id, subscription, doctype_name, doc_name,
			event, endpoint_url, attempt_num, status_code,
			success, latency_ms, error, creation
		FROM `tabLightning Webhook Log`
		ORDER BY creation DESC
		LIMIT 50
	""", as_dict=True)

	# 3. Summary stats
	stats = frappe.db.sql("""
		SELECT
			COUNT(*)                                                          AS total,
			SUM(CASE WHEN success = 1 THEN 1 ELSE 0 END)                    AS successful,
			SUM(CASE WHEN success = 0 THEN 1 ELSE 0 END)                    AS failed,
			SUM(CASE WHEN attempt_num > 1 THEN 1 ELSE 0 END)                AS retried,
			ROUND(AVG(latency_ms), 0)                                        AS avg_latency_ms,
			ROUND(SUM(CASE WHEN success=1 THEN 1 ELSE 0 END) * 100.0
			      / NULLIF(COUNT(*), 0), 1)                                  AS success_rate
		FROM `tabLightning Webhook Log`
		WHERE creation >= DATE_SUB(NOW(), INTERVAL 24 HOUR)
	""", as_dict=True)[0]

	# 4. Retry rate by attempt number (last 24h)
	retry_breakdown = frappe.db.sql("""
		SELECT attempt_num, COUNT(*) AS count
		FROM `tabLightning Webhook Log`
		WHERE creation >= DATE_SUB(NOW(), INTERVAL 24 HOUR)
		GROUP BY attempt_num
		ORDER BY attempt_num ASC
	""", as_dict=True)

	# 5. Hourly delivery volume for the last 24h (for the trend chart)
	hourly_trend = frappe.db.sql("""
		SELECT
			DATE_FORMAT(creation, '%%H:00') AS hour,
			COUNT(*)                         AS deliveries,
			SUM(CASE WHEN success=1 THEN 1 ELSE 0 END) AS succeeded,
			SUM(CASE WHEN success=0 THEN 1 ELSE 0 END) AS failed
		FROM `tabLightning Webhook Log`
		WHERE creation >= DATE_SUB(NOW(), INTERVAL 24 HOUR)
		GROUP BY DATE_FORMAT(creation, '%%Y-%%m-%%d %%H')
		ORDER BY creation ASC
	""", as_dict=True)

	return {
		"subscriptions":   subscriptions,
		"recent_logs":     recent_logs,
		"stats":           stats,
		"retry_breakdown": retry_breakdown,
		"hourly_trend":    hourly_trend,
	}


@frappe.whitelist()
def replay_webhook_delivery(delivery_id):
	"""Re-enqueue a specific webhook delivery for immediate retry."""
	if not frappe.has_permission("Lightning Webhook Log", "write"):
		frappe.throw(_("Not permitted"), frappe.PermissionError)

	log_entry = frappe.db.get_value(
		"Lightning Webhook Log",
		{"delivery_id": delivery_id},
		["subscription", "doctype_name", "doc_name", "event"],
		as_dict=True
	)
	if not log_entry:
		frappe.throw(_("Delivery not found: {0}").format(delivery_id))

	sub = frappe.db.get_value(
		"Lightning Webhook",
		log_entry.subscription,
		["endpoint_url", "secret_key"],
		as_dict=True
	)
	if not sub:
		frappe.throw(_("Subscription not found: {0}").format(log_entry.subscription))

	# Re-push a minimal event onto the Redis stream so the engine picks it up.
	import json
	site = frappe.local.site
	rdb  = frappe.cache()
	payload = {
		"site":       site,
		"doctype":    log_entry.doctype_name,
		"name":       log_entry.doc_name,
		"event":      log_entry.event,
		"data":       {},
		"replay_of":  delivery_id,
	}
	rdb.xadd(
		f"lightning:webhooks:{site}",
		{"payload": json.dumps(payload, default=str)},
		maxlen=100_000,
	)
	return {"status": "queued", "delivery_id": delivery_id}


@frappe.whitelist(allow_guest=True)
def log_search():
    """Fallback endpoint in case the local JS analytics ping fails, or if we want to store it securely from backend."""
    # We will log natively via the Go Proxy, but this allows direct DB writes via frail network fallback.
    pass
