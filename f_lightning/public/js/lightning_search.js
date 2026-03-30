/**
 * Lightning Search Client UI
 * Connects to the Go Proxy at :8765
 */

 class LightningSearch {
    constructor() {
        this.apiPort = 8765;
        this.isOpen = false;
        this.results = [];
        this.selectedIndex = -1;
        this.debounceTimer = null;

        this.initDOM();
        this.bindEvents();
    }

    initDOM() {
        // Prevent duplicate initialization
        if (document.getElementById('ls-overlay')) return;

        const html = `
            <div id="ls-overlay" class="ls-overlay">
                <div class="ls-modal">
                    <div class="ls-header">
                        <span class="ls-icon-search">⚡</span>
                        <input type="text" id="ls-input" class="ls-input" placeholder="Search invoices, customers, items... (e.g., 'unpaid invoices > 5k')" autocomplete="off" spellcheck="false" />
                        <div class="ls-meta" id="ls-meta">
                            <span class="ls-kbd">esc</span> to close
                        </div>
                    </div>
                    <div id="ls-body" class="ls-body" style="display: none;"></div>
                </div>
            </div>
        `;
        document.body.insertAdjacentHTML('beforeend', html);

        this.overlay = document.getElementById('ls-overlay');
        this.input = document.getElementById('ls-input');
        this.body = document.getElementById('ls-body');
        this.meta = document.getElementById('ls-meta');
    }

    bindEvents() {
        // Global Keyboard Shortcut (Cmd+K or Ctrl+K)
        document.addEventListener('keydown', (e) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
                e.preventDefault();
                this.toggle();
            }
        });

        // Close on escape or click outside
        this.overlay.addEventListener('click', (e) => {
            if (e.target === this.overlay) this.close();
        });

        // Search Input Handling
        this.input.addEventListener('input', (e) => {
            const query = e.target.value.trim();
            if (query.length === 0) {
                this.clear();
                return;
            }

            // Debounce 80ms
            clearTimeout(this.debounceTimer);
            this.debounceTimer = setTimeout(() => this.search(query), 80);
        });

        // Local Keyboard Navigation
        this.input.addEventListener('keydown', (e) => {
            if (!this.isOpen || this.results.length === 0) return;

            if (e.key === 'ArrowDown') {
                e.preventDefault();
                this.selectedIndex = Math.min(this.selectedIndex + 1, this.results.length - 1);
                this.renderSelection();
            } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                this.selectedIndex = Math.max(this.selectedIndex - 1, 0);
                this.renderSelection();
            } else if (e.key === 'Enter') {
                e.preventDefault();
                this.openSelected();
            } else if (e.key === 'Escape') {
                e.preventDefault();
                this.close();
            }
        });
    }

    toggle() {
        this.isOpen ? this.close() : this.open();
    }

    open() {
        this.isOpen = true;
        this.overlay.classList.add('visible');
        setTimeout(() => this.input.focus(), 50);
    }

    close() {
        this.isOpen = false;
        this.overlay.classList.remove('visible');
        this.clear();
        this.input.value = '';
        this.input.blur();
    }

    clear() {
        this.body.innerHTML = '';
        this.body.style.display = 'none';
        this.meta.innerHTML = `<span class="ls-kbd">esc</span> to close`;
        this.results = [];
        this.selectedIndex = -1;
    }

    async search(query) {
        try {
            // Include credentials so Frappe's `sid` cookie is sent to the Go proxy
            const res = await fetch(`http://${window.location.hostname}:${this.apiPort}/api/v1/search?q=${encodeURIComponent(query)}`, {
                credentials: 'include'
            });

            if (!res.ok) throw new Error('Search failed');

            const data = await res.json();
            this.render(data);
        } catch (err) {
            console.error('Lightning Search Error:', err);
            // Fallback to Frappe Native search UI gracefully
            this.body.innerHTML = `<div class="ls-empty">⚠️ Lightning service unavailable. Fallback to Frappe search.</div>`;
            this.body.style.display = 'block';
        }
    }

    render(data) {
        this.body.style.display = 'block';
        
        if (!data.hits || data.hits.length === 0) {
            this.body.innerHTML = `<div class="ls-empty">No results found natively in Meilisearch.</div>`;
            this.results = [];
            return;
        }

        // Display parsed NLP syntax in the top right meta bar
        const p = data.parsed;
        let metaHtml = `<span style="color:var(--ls-text-muted)">⚡ ${data.took_ms}ms</span>`;
        if (p) {
            if (p.doctype) metaHtml += `<span class="ls-pill">${p.doctype}</span>`;
            if (p.filters && p.filters.length) metaHtml += `<span class="ls-pill">Filters: ${p.filters.length}</span>`;
        }
        this.meta.innerHTML = metaHtml;

        // Group Results by DocType
        const grouped = {};
        data.hits.forEach((hit) => {
            const dt = hit.doctype || 'Document';
            if (!grouped[dt]) grouped[dt] = [];
            grouped[dt].push(hit);
        });

        let html = '';
        this.results = []; // Flat array for arrow navigation

        for (const [dt, items] of Object.entries(grouped)) {
            html += `<div class="ls-group-header">${dt}</div>`;
            items.forEach(item => {
                this.results.push(item);
                const idx = this.results.length - 1;
                
                // Construct Title based on DocType
                let title = item.name;
                let subtitle = item.customer || item.supplier || item.owner || 'No subtitle';
                
                if (item.customer_name) title = item.customer_name;
                if (item.item_name) title = item.item_name;

                html += `
                    <div class="ls-item" id="ls-item-${idx}" onclick="frappe.lightning.go(${idx})">
                        <div class="ls-item-icon">${dt.charAt(0).toUpperCase()}</div>
                        <div class="ls-item-content">
                            <div class="ls-item-title">${title} <span style="font-size:0.7em; color:gray">${item.name}</span></div>
                            <div class="ls-item-subtitle">${subtitle}</div>
                        </div>
                    </div>
                `;
            });
        }

        this.body.innerHTML = html;
        this.selectedIndex = 0;
        this.renderSelection();
    }

    renderSelection() {
        document.querySelectorAll('.ls-item').forEach(el => el.classList.remove('selected'));
        if (this.selectedIndex >= 0) {
            const el = document.getElementById(`ls-item-${this.selectedIndex}`);
            if (el) {
                el.classList.add('selected');
                el.scrollIntoView({ block: 'nearest' });
            }
        }
    }

    openSelected() {
        if (this.selectedIndex >= 0 && this.results[this.selectedIndex]) {
            this.go(this.selectedIndex);
        }
    }

    go(index) {
        const item = this.results[index];
        if (!item) return;

        // Optionally send a click analytics ping
        this.logClick(item);

        // Frappe routing
        const dt = item.doctype;
        const name = item.name;
        this.close();
        frappe.set_route('Form', dt, name);
    }

    logClick(item) {
        fetch(`http://${window.location.hostname}:${this.apiPort}/api/v1/analytics/click`, {
            method: 'POST',
            credentials: 'include',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                doctype: item.doctype,
                name: item.name,
                query: this.input.value
            })
        }).catch(() => {}); // fire and forget
    }
}

// Initialize when Frappe is ready
$(document).ready(() => {
    // Only run on Desk (App) side, not Website
    if (window.frappe && !window.frappe.boot?.is_website) {
        window.frappe.lightning = new LightningSearch();
        
        // Hijack default search bar click
        setTimeout(() => {
            const defaultSearch = document.querySelector('.search-bar, .awesomplete');
            if(defaultSearch) {
                defaultSearch.addEventListener('click', (e) => {
                    e.preventDefault();
                    e.stopPropagation();
                    window.frappe.lightning.open();
                }, true);
            }
        }, 1000);
    }
});
