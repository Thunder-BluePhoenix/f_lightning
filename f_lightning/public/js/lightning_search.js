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
        this.cache = new SearchCache('ls_cache', 5); // 5 minute TTL

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
                        <input type="text" id="ls-input" class="ls-input" placeholder="Search invoices, customers, items..." autocomplete="off" spellcheck="false" />
                        <div class="ls-meta" id="ls-meta">
                            <span class="ls-action-btn" id="ls-btn-save" title="Save Search">⭐</span>
                            <span class="ls-action-btn" id="ls-btn-pin" title="Pin Search">📌</span>
                            <span class="ls-kbd" style="margin-left:10px">esc</span>
                        </div>
                    </div>
                    <div id="ls-body" class="ls-body" style="display: none;">
                        <div id="ls-list" class="ls-list"></div>
                        <div id="ls-preview" class="ls-preview">
                            <div class="ls-empty">Select a result to preview</div>
                        </div>
                        <div id="ls-detail-overlay" class="ls-detail-overlay"></div>
                    </div>
                </div>
            </div>
        `;
        document.body.insertAdjacentHTML('beforeend', html);

        this.overlay = document.getElementById('ls-overlay');
        this.input = document.getElementById('ls-input');
        this.body = document.getElementById('ls-body');
        this.list = document.getElementById('ls-list');
        this.preview = document.getElementById('ls-preview');
        this.detailOverlay = document.getElementById('ls-detail-overlay');
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

        // Close detail overlay on click
        this.detailOverlay.addEventListener('click', () => {
            this.detailOverlay.classList.remove('visible');
        });

        // Save / Pin Buttons
        document.getElementById('ls-btn-save').addEventListener('click', () => this.saveCurrent(false));
        document.getElementById('ls-btn-pin').addEventListener('click', () => this.saveCurrent(true));

        // Search Input Handling
        this.input.addEventListener('input', (e) => {
            const query = e.target.value.trim();
            if (query.length === 0) {
                this.loadSaved(); // Show history when empty
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
                if (this.detailOverlay.classList.contains('visible')) {
                    this.detailOverlay.classList.remove('visible');
                } else {
                    this.close();
                }
            }
        });
    }

    toggle() {
        this.isOpen ? this.close() : this.open();
    }

    open() {
        this.isOpen = true;
        this.overlay.classList.add('visible');
        this.loadSaved(); // Show recent/pinned on open
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
        this.list.innerHTML = '';
        this.preview.innerHTML = '<div class="ls-empty">Select a result to preview</div>';
        this.detailOverlay.innerHTML = '';
        this.detailOverlay.classList.remove('visible');
        this.body.style.display = 'none';
        this.results = [];
        this.selectedIndex = -1;
    }

    async search(query) {
        try {
            const cached = await this.cache.get(query);
            if (cached) {
                this.renderSearchResults(cached, true);
                return;
            }

            const res = await fetch(`http://${window.location.hostname}:${this.apiPort}/api/v1/search?q=${encodeURIComponent(query)}`, {
                credentials: 'include'
            });

            if (!res.ok) throw new Error('Search failed');
            const data = await res.json();
            await this.cache.set(query, data);
            
            this.renderSearchResults(data);
        } catch (err) {
            console.error('Lightning Search Error:', err);
            this.list.innerHTML = `<div class="ls-empty">⚠️ Service unavailable.</div>`;
            this.body.style.display = 'block';
        }
    }

    renderSearchResults(data, isCached = false) {
        this.body.style.display = 'flex';
        
        if (!data.hits || data.hits.length === 0) {
            this.list.innerHTML = `<div class="ls-empty">No results found.</div>`;
            this.results = [];
            return;
        }

        const p = data.parsed;
        let metaHtml = `<span style="color:var(--ls-text-muted)">⚡ ${isCached ? 'cached' : data.took_ms + 'ms'}</span>`;
        if (p && p.doctype) metaHtml += `<span class="ls-pill">${p.doctype}</span>`;
        // Keep the save/pin buttons but prefix with stats
        const originalMeta = `<span class="ls-action-btn" id="ls-btn-save" title="Save Search">⭐</span>
                             <span class="ls-action-btn" id="ls-btn-pin" title="Pin Search">📌</span>`;
        this.meta.innerHTML = metaHtml + originalMeta;
        // Re-bind because we just overwrote the HTML
        document.getElementById('ls-btn-save').onclick = () => this.saveCurrent(false);
        document.getElementById('ls-btn-pin').onclick = () => this.saveCurrent(true);

        const grouped = {};
        data.hits.forEach((hit) => {
            const dt = hit.doctype || 'Document';
            if (!grouped[dt]) grouped[dt] = [];
            grouped[dt].push(hit);
        });

        let html = '';
        this.results = [];

        for (const [dt, items] of Object.entries(grouped)) {
            html += `<div class="ls-group-header">${dt}</div>`;
            items.forEach(item => {
                this.results.push(item);
                const idx = this.results.length - 1;
                let title = item.item_name || item.customer_name || item.name;
                let subtitle = item.customer || item.supplier || item.owner || '';

                html += `
                    <div class="ls-item" id="ls-item-${idx}" onclick="frappe.lightning.go(${idx})">
                        <div class="ls-item-icon">${dt.charAt(0).toUpperCase()}</div>
                        <div class="ls-item-content">
                            <div class="ls-item-title">${title}</div>
                            <div class="ls-item-subtitle">${subtitle}</div>
                        </div>
                        <div class="ls-mobile-only ls-action-btn" onclick="event.stopPropagation(); frappe.lightning.showDetail(${idx})">ℹ️</div>
                    </div>
                `;
            });
        }

        this.list.innerHTML = html;
        this.selectedIndex = 0;
        this.renderSelection();
    }

    renderSelection() {
        document.querySelectorAll('.ls-item').forEach(el => el.classList.remove('selected'));
        if (this.selectedIndex >= 0 && this.results[this.selectedIndex]) {
            const el = document.getElementById(`ls-item-${this.selectedIndex}`);
            if (el) {
                el.classList.add('selected');
                el.scrollIntoView({ block: 'nearest' });
            }

            const item = this.results[this.selectedIndex];
            this.updatePreview(item, this.preview);
            this.updatePreview(item, this.detailOverlay);
        }
    }

    updatePreview(item, container) {
        let fieldsHtml = '';
        const skip = ['_vectors', 'doctype', 'name', 'default_click_score'];
        
        for (const [key, val] of Object.entries(item)) {
            if (skip.includes(key) || !val) continue;
            fieldsHtml += `
                <div class="ls-preview-field">
                    <div class="ls-preview-label">${key.replace(/_/g, ' ')}</div>
                    <div class="ls-preview-value">${val}</div>
                </div>
            `;
        }

        container.innerHTML = `
            <div class="ls-preview-header">
                <div class="ls-preview-label">${item.doctype}</div>
                <div class="ls-preview-title">${item.name}</div>
            </div>
            ${fieldsHtml}
            <div style="margin-top:auto; padding-top:20px">
                <button class="btn btn-primary btn-sm btn-block" onclick="frappe.lightning.openSelected()">Open Document</button>
            </div>
        `;
    }

    showDetail(idx) {
        this.selectedIndex = idx;
        this.renderSelection();
        this.detailOverlay.classList.add('visible');
    }

    async saveCurrent(pinned = false) {
        const query = this.input.value.trim();
        if (!query) return;

        try {
            await frappe.call({
                method: 'frappe.client.insert',
                args: {
                    doc: {
                        doctype: 'Lightning Saved Search',
                        user: frappe.session.user,
                        label: query,
                        query: query,
                        is_pinned: pinned ? 1 : 0,
                        timestamp: frappe.datetime.now_datetime()
                    }
                }
            });
            frappe.show_alert({ message: pinned ? 'Pinned!' : 'Saved!', indicator: 'green' });
        } catch (e) {
            console.error(e);
        }
    }

    async loadSaved() {
        try {
            const res = await frappe.call({
                method: 'frappe.client.get_list',
                args: {
                    doctype: 'Lightning Saved Search',
                    filters: { user: frappe.session.user },
                    fields: ['query', 'label', 'is_pinned', 'name'],
                    order_by: 'is_pinned desc, timestamp desc',
                    limit: 10
                }
            });
            this.renderSaved(res.message || []);
        } catch (e) {
            this.clear();
        }
    }

    renderSaved(items) {
        this.body.style.display = 'flex';
        if (items.length === 0) {
            this.list.innerHTML = `<div class="ls-empty">Start searching to see history.</div>`;
            return;
        }

        let html = '<div class="ls-group-header">Recent & Pinned</div>';
        this.results = [];
        
        items.forEach((item, idx) => {
            html += `
                <div class="ls-item" id="ls-item-${idx}" onclick="window.frappe.lightning.input.value='${item.query}'; window.frappe.lightning.search('${item.query}')">
                    <div class="ls-item-icon">${item.is_pinned ? '📌' : '🕒'}</div>
                    <div class="ls-item-content">
                        <div class="ls-item-title">${item.label}</div>
                    </div>
                </div>
            `;
        });
        this.list.innerHTML = html;
        this.preview.innerHTML = '<div class="ls-empty">Select a history item to re-run search</div>';
    }

    openSelected() {
        if (this.selectedIndex >= 0 && this.results[this.selectedIndex]) {
            this.go(this.selectedIndex);
        }
    }

    go(index) {
        const item = this.results[index];
        if (!item) return;
        this.logClick(item);
        this.close();
        frappe.set_route('Form', item.doctype, item.name);
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
        }).catch(() => {});
    }
}

/**
 * SearchCache helper for IndexedDB persistence
 */
class SearchCache {
    constructor(dbName, ttlMinutes) {
        this.dbName = dbName;
        this.ttl = ttlMinutes * 60 * 1000;
        this.db = null;
    }

    async getDb() {
        if (this.db) return this.db;
        return new Promise((resolve, reject) => {
            const request = indexedDB.open(this.dbName, 1);
            request.onupgradeneeded = (e) => {
                const db = e.target.result;
                if (!db.objectStoreNames.contains('search')) {
                    db.createObjectStore('search', { keyPath: 'query' });
                }
            };
            request.onsuccess = (e) => {
                this.db = e.target.result;
                resolve(this.db);
            };
            request.onerror = (e) => reject(e.target.error);
        });
    }

    async get(query) {
        const db = await this.getDb();
        return new Promise((resolve) => {
            const transaction = db.transaction(['search'], 'readonly');
            const store = transaction.objectStore('search');
            const request = store.get(query);
            request.onsuccess = () => {
                const result = request.result;
                if (result && (Date.now() - result.timestamp < this.ttl)) {
                    resolve(result.data);
                } else {
                    resolve(null);
                }
            };
            request.onerror = () => resolve(null);
        });
    }

    async set(query, data) {
        const db = await this.getDb();
        return new Promise((resolve) => {
            const transaction = db.transaction(['search'], 'readwrite');
            const store = transaction.objectStore('search');
            store.put({
                query: query,
                data: data,
                timestamp: Date.now()
            });
            transaction.oncomplete = () => resolve();
        });
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
