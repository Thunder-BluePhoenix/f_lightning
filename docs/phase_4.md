# Phase 4: Frappe Frontend UI & Analytics

**Goal:** Inject a premium, VS Code-style Cmd+K Command Palette into Frappe Desk and build a Search Analytics Dashboard inside the app. This is the phase users see — it needs to be polished enough to go viral in the Frappe community.

---

## Frappe Hooks Integration

```python
# f_lightning/hooks.py
app_include_js  = ["lightning/js/lightning_search.bundle.js"]
app_include_css = ["lightning/css/lightning_search.css"]

# Analytics DocType
doctype_list_js = {
    "Lightning Search Log": "lightning/js/search_log_list.js"
}
```

The JS bundle is injected into every Frappe Desk page globally — no page-specific wiring needed.

---

## The Omnibox UI — Design Spec

```
┌──────────────────────────────────────────────────────┐
│  ⚡  Search anything... (Cmd+K)             [Esc ✕]  │
├──────────────────────────────────────────────────────┤
│ 🏷 Sales Invoices                                    │
│  › ACC-SINV-2026-00123  Amazon India  ₹1,25,000    │
│  › ACC-SINV-2026-00119  Flipkart      ₹98,000       │
│─────────────────────────────────────────────────────│
│ 👤 Customers                                         │
│  › Amazon India Pvt Ltd               [Customer]    │
│  › Flipkart India                     [Customer]    │
│─────────────────────────────────────────────────────│
│ 📦 Items                                             │
│  › Laptop Pro 15"                     ₹75,000       │
│─────────────────────────────────────────────────────│
│  ↑ ↓ Navigate   Enter Open   Esc Close   ⚡ <6ms    │
└──────────────────────────────────────────────────────┘
```

---

## JS Component

```javascript
// f_lightning/public/js/lightning_search.js

class LightningSearch {
    constructor() {
        this.overlay  = null;
        this.input    = null;
        this.results  = null;
        this.debounce = null;
        this.selected = 0;
        this.hits     = [];
        this.init();
    }

    init() {
        this.buildDOM();
        document.addEventListener('keydown', (e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                this.open();
            }
            if (e.key === 'Escape')    this.close();
            if (e.key === 'ArrowDown') this.navigate(1);
            if (e.key === 'ArrowUp')   this.navigate(-1);
            if (e.key === 'Enter')     this.openSelected();
        });
    }

    buildDOM() {
        this.overlay = document.createElement('div');
        this.overlay.className = 'lightning-overlay hidden';
        this.overlay.innerHTML = `
            <div class="lightning-modal" role="dialog" aria-label="Lightning Search">
                <div class="lightning-header">
                    <span class="lightning-icon">⚡</span>
                    <input class="lightning-input"
                           id="lightning-search-input"
                           type="text"
                           placeholder="Search anything... try 'unpaid invoices above 10k'"
                           autocomplete="off"
                           spellcheck="false" />
                    <kbd class="lightning-esc-hint">Esc</kbd>
                </div>
                <div class="lightning-divider"></div>
                <div class="lightning-results" id="lightning-results"></div>
                <div class="lightning-footer">
                    <span><kbd>↑↓</kbd> Navigate</span>
                    <span><kbd>↵</kbd> Open</span>
                    <span><kbd>Esc</kbd> Close</span>
                    <span class="lightning-latency" id="lightning-latency"></span>
                </div>
            </div>
        `;
        document.body.appendChild(this.overlay);

        this.input   = this.overlay.querySelector('#lightning-search-input');
        this.results = this.overlay.querySelector('#lightning-results');

        this.input.addEventListener('input', (e) => this.search(e.target.value));
        this.overlay.addEventListener('click', (e) => {
            if (e.target === this.overlay) this.close();
        });
    }

    open() {
        this.overlay.classList.remove('hidden');
        this.selected = 0;
        this.hits = [];
        this.results.innerHTML = '';
        setTimeout(() => this.input.focus(), 50);
    }

    close() {
        this.overlay.classList.add('hidden');
        this.input.value = '';
        this.results.innerHTML = '';
    }

    search(query) {
        clearTimeout(this.debounce);
        if (!query.trim()) {
            this.results.innerHTML = '';
            return;
        }
        this.debounce = setTimeout(async () => {
            const t0 = performance.now();
            try {
                const res = await fetch(
                    `/api/v1/search?q=${encodeURIComponent(query)}`,
                    { credentials: 'include' }  // Sends Frappe sid cookie
                );
                const data = await res.json();
                const tookMs = Math.round(performance.now() - t0);

                this.hits = data.hits || [];
                this.render(data.hits, data.parsed, tookMs);

                // Log analytics
                this.logSearch(query, data.hits?.length, tookMs);
            } catch (err) {
                this.renderError();
            }
        }, 80); // 80ms debounce — tight for snappy feel
    }

    render(hits, parsed, tookMs) {
        const latency = document.getElementById('lightning-latency');
        latency.textContent = `⚡ ${tookMs}ms`;

        if (!hits || hits.length === 0) {
            this.results.innerHTML = `<div class="lightning-empty">No results for "${this.input.value}"</div>`;
            return;
        }

        // Group by DocType
        const groups = {};
        hits.forEach(hit => {
            if (!groups[hit.doctype]) groups[hit.doctype] = [];
            groups[hit.doctype].push(hit);
        });

        let html = '';
        let idx = 0;
        for (const [doctype, docs] of Object.entries(groups)) {
            html += `<div class="lightning-group-label">${this.getIcon(doctype)} ${doctype}</div>`;
            docs.forEach(doc => {
                html += `
                    <div class="lightning-result-item ${idx === 0 ? 'selected' : ''}"
                         data-index="${idx}"
                         data-doctype="${doc.doctype}"
                         data-name="${doc.name}"
                         onclick="lightningSearch.openDoc('${doc.doctype}', '${doc.name}')">
                        <span class="lightning-doc-name">${doc.name}</span>
                        <span class="lightning-doc-meta">${doc.customer_name || doc.item_name || ''}</span>
                        <span class="lightning-doc-value">${doc.grand_total_display || ''}</span>
                    </div>`;
                idx++;
            });
        }
        this.results.innerHTML = html;
    }

    navigate(dir) {
        this.selected = Math.max(0, Math.min(this.hits.length - 1, this.selected + dir));
        this.results.querySelectorAll('.lightning-result-item').forEach((el, i) => {
            el.classList.toggle('selected', i === this.selected);
            if (i === this.selected) el.scrollIntoView({ block: 'nearest' });
        });
    }

    openSelected() {
        const hit = this.hits[this.selected];
        if (hit) this.openDoc(hit.doctype, hit.name);
    }

    openDoc(doctype, name) {
        frappe.set_route('Form', doctype, name);
        this.logClick(doctype, name);
        this.close();
    }

    getIcon(doctype) {
        const icons = {
            'Sales Invoice': '🏷', 'Customer': '👤', 'Item': '📦',
            'Purchase Order': '🛒', 'Supplier': '🏭', 'Lead': '🎯',
        };
        return icons[doctype] || '📄';
    }

    async logSearch(query, count, tookMs) {
        // Fire-and-forget analytics log
        fetch('/api/method/f_lightning.api.log_search', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json', 'X-Frappe-CSRF-Token': frappe.csrf_token },
            body: JSON.stringify({ query, result_count: count, took_ms: tookMs }),
        }).catch(() => {}); // Silent fail — never block search UX
    }

    async logClick(doctype, name) {
        fetch('/api/v1/analytics/click', {
            method: 'POST',
            credentials: 'include',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ doctype, name, query: this.input.value }),
        }).catch(() => {});
    }

    renderError() {
        this.results.innerHTML = `<div class="lightning-empty lightning-error">Search unavailable — falling back to Frappe search</div>`;
    }
}

const lightningSearch = new LightningSearch();
```

---

## CSS — Dark/Light Mode

```css
/* f_lightning/public/css/lightning_search.css */

.lightning-overlay {
    position: fixed;
    inset: 0;
    background: rgba(0,0,0,0.5);
    backdrop-filter: blur(6px);
    -webkit-backdrop-filter: blur(6px);
    z-index: 9999;
    display: flex;
    align-items: flex-start;
    justify-content: center;
    padding-top: 10vh;
    animation: fadeIn 0.12s ease;
}

.lightning-overlay.hidden { display: none; }

.lightning-modal {
    width: 640px;
    max-width: calc(100vw - 40px);
    background: var(--bg-color);
    border: 1px solid var(--border-color);
    border-radius: 14px;
    box-shadow: 0 32px 100px rgba(0,0,0,0.4), 0 0 0 1px rgba(255,255,255,0.05);
    overflow: hidden;
    animation: slideDown 0.15s cubic-bezier(0.16, 1, 0.3, 1);
}

.lightning-header {
    display: flex;
    align-items: center;
    padding: 0 16px;
    gap: 12px;
}

.lightning-icon { font-size: 20px; }

.lightning-input {
    flex: 1;
    padding: 18px 0;
    font-size: 18px;
    border: none;
    outline: none;
    background: transparent;
    color: var(--text-color);
    font-family: inherit;
}

.lightning-divider { height: 1px; background: var(--border-color); }

.lightning-results {
    max-height: 400px;
    overflow-y: auto;
    padding: 8px 0;
}

.lightning-group-label {
    padding: 10px 20px 4px;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--text-muted);
}

.lightning-result-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 10px 20px;
    cursor: pointer;
    transition: background 0.08s;
    border-radius: 6px;
    margin: 0 6px;
}
.lightning-result-item:hover,
.lightning-result-item.selected {
    background: var(--control-bg-on-gray);
}

.lightning-doc-name  { font-weight: 600; font-size: 14px; flex: 0 0 auto; }
.lightning-doc-meta  { color: var(--text-muted); font-size: 13px; flex: 1; }
.lightning-doc-value { font-size: 13px; font-weight: 500; color: var(--text-color); }

.lightning-footer {
    display: flex;
    gap: 16px;
    padding: 10px 20px;
    border-top: 1px solid var(--border-color);
    font-size: 12px;
    color: var(--text-muted);
    align-items: center;
}
.lightning-latency { margin-left: auto; color: var(--green); font-weight: 600; }

.lightning-empty { padding: 32px 20px; text-align: center; color: var(--text-muted); font-size: 14px; }
.lightning-error { color: var(--red-avatar); }

kbd {
    display: inline-block;
    padding: 2px 6px;
    border: 1px solid var(--border-color);
    border-radius: 4px;
    font-size: 11px;
    background: var(--subtle-bg);
}

@keyframes fadeIn    { from { opacity: 0; } to { opacity: 1; } }
@keyframes slideDown {
    from { transform: translateY(-16px) scale(0.98); opacity: 0; }
    to   { transform: translateY(0) scale(1); opacity: 1; }
}
```

---

## Search Analytics — DocType

A Frappe DocType `Lightning Search Log` stores every search event:

| Field | Type | Description |
|---|---|---|
| `query` | Data | The raw string searched |
| `user` | Link → User | Frappe user who searched |
| `doctype_hit` | Data | Primary DocType returned |
| `result_count` | Int | How many hits were returned |
| `took_ms` | Int | Query latency in milliseconds |
| `clicked_doc` | Data | Document name opened (if any) |
| `timestamp` | Datetime | When the search happened |
| `site` | Data | Site name (for multi-tenant) |

---

## Analytics Dashboard (Frappe Page)

A dedicated **Frappe Page** (`/lightning-analytics`) accessible from the sidebar:

### Dashboard Panels

| Panel | Type | Query |
|---|---|---|
| **Top Searches** | Bar chart | `GROUP BY query ORDER BY COUNT(*) DESC LIMIT 20` |
| **Zero-Result Queries** | Table | `WHERE result_count = 0 ORDER BY COUNT(*) DESC` |
| **Average Latency Trend** | Line chart | `AVG(took_ms) GROUP BY DATE(timestamp)` |
| **Click-Through Rate** | KPI | `COUNT(clicked_doc IS NOT NULL) / COUNT(*) * 100` |
| **Search Volume by Role** | Pie chart | JOIN with User roles |
| **Slowest Queries** | Table | `ORDER BY took_ms DESC LIMIT 10` |

```python
# f_lightning/page/lightning_analytics/lightning_analytics.py
import frappe

@frappe.whitelist()
def get_analytics_data(days=30):
    return {
        "top_queries": frappe.db.sql("""
            SELECT query, COUNT(*) as count, AVG(took_ms) as avg_ms
            FROM `tabLightning Search Log`
            WHERE timestamp >= DATE_SUB(NOW(), INTERVAL %s DAY)
            GROUP BY query ORDER BY count DESC LIMIT 20
        """, days, as_dict=True),

        "zero_results": frappe.db.sql("""
            SELECT query, COUNT(*) as count
            FROM `tabLightning Search Log`
            WHERE result_count = 0
            AND timestamp >= DATE_SUB(NOW(), INTERVAL %s DAY)
            GROUP BY query ORDER BY count DESC LIMIT 20
        """, days, as_dict=True),

        "latency_trend": frappe.db.sql("""
            SELECT DATE(timestamp) as date, AVG(took_ms) as avg_ms, COUNT(*) as searches
            FROM `tabLightning Search Log`
            WHERE timestamp >= DATE_SUB(NOW(), INTERVAL %s DAY)
            GROUP BY DATE(timestamp) ORDER BY date
        """, days, as_dict=True),
    }
```

---

## Validation Checklist

- [ ] Pressing `Cmd+K` (Mac) and `Ctrl+K` (Windows/Linux) opens the overlay globally
- [ ] Pressing `Esc` closes the overlay cleanly and clears the input
- [ ] Typing queries returns grouped results categorized by DocType within 100ms visually
- [ ] Arrow key navigation highlights result items correctly
- [ ] `Enter` opens the selected document in Frappe form view and closes the modal
- [ ] Dark mode and light mode both render correctly using Frappe CSS variables
- [ ] Latency display shows `⚡ Xms` in the footer
- [ ] Click on overlay background closes the modal
- [ ] `Lightning Search Log` DocType exists and entries are written per search
- [ ] Analytics dashboard page loads, all 4 panels render with real data
- [ ] Zero-result queries list is populated and sorted by frequency
- [ ] Graceful degradation: if Go proxy is down, error message shown (no blank screen)
