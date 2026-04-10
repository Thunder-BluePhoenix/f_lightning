"""
Lightning Webhook Enqueue Hook
------------------------------
Called by Frappe's doc_events for every document event.
Serialises the document payload and pushes it onto a Redis Stream so the
Go webhook engine can pick it up asynchronously without blocking the save.
"""

import json
import frappe


def enqueue(doc, method):
	"""Push a document event onto the Lightning webhook Redis Stream.

	This function is intentionally minimal and non-blocking: it does a single
	Redis XADD and returns. All HTTP delivery, retries, and logging happen in
	the Go engine.

	Args:
		doc:    Frappe document instance (has .doctype, .name, .as_dict()).
		method: Event name as a string, e.g. "on_update", "after_insert".
	"""
	try:
		site = frappe.local.site
		rdb = frappe.cache()

		payload = {
			"site":    site,
			"doctype": doc.doctype,
			"name":    doc.name,
			"event":   method,
			"data":    doc.as_dict(),
		}

		rdb.xadd(
			f"lightning:webhooks:{site}",
			{"payload": json.dumps(payload, default=str)},
			maxlen=100_000,
		)
	except Exception:
		# Never let a webhook error break a document save.
		frappe.log_error(frappe.get_traceback(), "Lightning Webhook Enqueue Error")
