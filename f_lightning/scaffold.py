import frappe

def create_scaffolding():
    # Create DocType
    if not frappe.db.exists("DocType", "Lightning Search Log"):
        print("Creating Lightning Search Log DocType...")
        doc = frappe.get_doc({
            "doctype": "DocType",
            "module": "Frappe Lightning",
            "custom": 0,  # We want this tracked in the app repository
            "name": "Lightning Search Log",
            "is_submittable": 0,
            "autoname": "hash",  # Fast inserts
            "permissions": [
                {
                    "role": "System Manager",
                    "read": 1,
                    "write": 1,
                    "create": 1,
                    "delete": 1
                }
            ],
            "fields": [
                {"fieldname": "query", "label": "Query", "fieldtype": "Data", "reqd": 1, "in_list_view": 1},
                {"fieldname": "user", "label": "User", "fieldtype": "Link", "options": "User", "in_list_view": 1},
                {"fieldname": "site", "label": "Site", "fieldtype": "Data", "hidden": 1},
                {"fieldname": "column_break_1", "fieldtype": "Column Break"},
                {"fieldname": "hits", "label": "Hits", "fieldtype": "Int", "in_list_view": 1},
                {"fieldname": "latency_ms", "label": "Latency (ms)", "fieldtype": "Int", "in_list_view": 1},
                {"fieldname": "section_break_1", "fieldtype": "Section Break", "label": "Click Target"},
                {"fieldname": "clicked_doctype", "label": "Clicked DocType", "fieldtype": "Link", "options": "DocType"},
                {"fieldname": "clicked_name", "label": "Clicked Name", "fieldtype": "Data"}
            ]
        })
        doc.insert(ignore_permissions=True)
        frappe.db.commit()
    
    # Create Page
    if not frappe.db.exists("Page", "lightning-analytics"):
        print("Creating lightning-analytics Page...")
        doc = frappe.get_doc({
            "doctype": "Page",
            "module": "Frappe Lightning",
            "page_name": "lightning-analytics",
            "title": "Lightning Search Analytics",
            "standard": "Yes"
        })
        doc.insert(ignore_permissions=True)
        frappe.db.commit()

    print("Scaffolding complete.")
