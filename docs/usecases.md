# 🌟 Frappe Lightning: Use Cases & Magic Queries

Frappe Lightning isn't just a search bar; it's a productivity engine. Here are real-world scenarios where Lightning transforms the way you interact with your data.

---

## 💰 1. Finance & Accounting
Finding specific financial records in a sea of millions can be daunting. Lightning makes it effortless.

- **"Find overdue invoices for Amazon > 50k"**
  - *Behind the scenes*: Filters for `Customer: Amazon`, `Status: Overdue`, and `Grand Total > 50,000`.
- **"Unpaid invoices from last month"**
  - *Behind the scenes*: Combines `Status: Overdue` with a `Posting Date` range for the previous month.
- **"Payments yesterday"**
  - *Behind the scenes*: Instantly pulls up all `Payment Entry` records created on the previous date.

---

## 🤝 2. Sales & CRM
Closing deals requires fast access to information about customers and leads.

- **"Active leads in Mumbai"**
  - *Behind the scenes*: Searches for `Lead` DocType with `Status: Open` and `City: Mumbai`.
- **"Recent customers"**
  - *Behind the scenes*: Uses ranking weights to surface newly created or frequently accessed `Customer` records first.
- **"Item: MacBook Pro in stock"**
  - *Behind the scenes*: A multi-index query that checks for the item and its current stock level in real-time.

---

## 📦 3. Inventory & Procurement
Managing thousands of SKUs and suppliers is a breeze with Lightning's sub-10ms response time.

- **"Suppliers with pending POs"**
  - *Behind the scenes*: Cross-references `Supplier` records with open `Purchase Order` documents.
- **"Items below 500"**
  - *Behind the scenes*: Filters the `Item` index for a `Standard Rate` or `Valuation Rate` less than 500.
- **"SKU: APP-LPT-001"**
  - *Behind the scenes*: Instant exact-match lookup for warehouse staff.

---

## 🏗️ 4. Multi-Tenant Operations
Large organizations running multiple Frappe sites can use a single Lightning sidecar service to power search for everyone.

- **Isolated Site Search**: Users on `site1.example.com` will *never* see results from `site2.example.com`.
- **Role-Based Access**: A sales manager in the "North" branch only sees customers they are permitted to view, even if the search index contains global data.

---

## 🪄 5. "Magic Queries" — The NLP Showcase
These are "wow" queries that showcase the power of the rule-based NLP engine.

| Natural Language Query | Interpreted Action |
|---|---|
| `"10k to 50k invoices"` | `grand_total BETWEEN 10000 AND 50000` |
| `"overdue since 2026-01-01"` | `status = "Overdue" AND posting_date >= "2026-01-01"` |
| `"this week's sales"` | `doctype = "Sales Invoice" AND posting_date = CURRENT_WEEK` |
| `"unpaid for Apple"` | `status = "Overdue" AND (customer = "Apple" OR name ~ "Apple")` |
| `"draft POs > 1cr"` | `doctype = "Purchase Order" AND status = "Draft" AND grand_total > 10,000,000` |

---

## 💻 6. Developer Experience
Use Lightning as a platform, not just a plugin.

- **Custom Dashboards**: Fetch search results via REST API and display them in custom-built React or Vue.js dashboards.
- **Mobile Apps**: Lightning's API is optimized for mobile performance, making it the perfect backend for an ERP mobile extension.
- **Integrations**: Connect your internal tools (like Slack bots) to search your Frappe data in real-time via the Lightning Go proxy.

---

> [!TIP]
> Lightning's NLP engine supports Indian notations like **"lakh" (1e5)** and **"crore" (1e7)** natively! Try searching for "invoices above 5 lakhs".
