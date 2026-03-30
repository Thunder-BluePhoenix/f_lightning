frappe.pages['lightning-analytics'].on_page_load = function(wrapper) {
	var page = frappe.ui.make_app_page({
		parent: wrapper,
		title: 'Lightning Analytics',
		single_column: true
	});

	page.set_indicator('Fetching...', 'orange');

	$(wrapper).bind('show', function() {
		load_analytics_data(page);
	});
}

function load_analytics_data(page) {
	frappe.call({
		method: 'f_lightning.frappe_lightning.api.get_analytics_data',
		args: { days: 30 },
		callback: function(r) {
			if (r.message) {
				page.set_indicator('Live', 'green');
				render_dashboard(page, r.message);
			}
		}
	});
}

function render_dashboard(page, data) {
	var html = `
		<style>
			.la-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 20px; padding: 20px; }
			.la-card { background: var(--card-bg); border-radius: 8px; padding: 20px; box-shadow: var(--shadow-sm); border: 1px solid var(--border-color); }
			.la-huge { font-size: 2.5rem; font-weight: 700; color: var(--text-color); margin: 10px 0; }
			.la-label { text-transform: uppercase; font-size: 0.8rem; letter-spacing: 0.05em; color: var(--text-muted); font-weight: 600; }
			.la-table { width: 100%; border-collapse: collapse; margin-top: 15px; }
			.la-table th, .la-table td { padding: 8px; text-align: left; border-bottom: 1px solid var(--border-color); font-size: 0.9rem; }
			.la-table th { color: var(--text-muted); font-weight: 500; }
			.la-pill { display: inline-block; padding: 2px 8px; border-radius: 12px; background: rgba(var(--primary-color-rgb), 0.1); color: var(--primary-color); font-size: 0.75rem; }
		</style>
		<div class="la-grid">
			<!-- Metrics -->
			<div class="la-card">
				<div class="la-label">Total Searches (30d)</div>
				<div class="la-huge">${data.metrics.total_searches || 0}</div>
				<div class="text-muted">Searches routed via Go Proxy</div>
			</div>
			<div class="la-card">
				<div class="la-label">Average Latency</div>
				<div class="la-huge">${data.metrics.avg_latency ? parseInt(data.metrics.avg_latency) : 0} <span style="font-size:1rem;color:var(--text-muted)">ms</span></div>
				<div class="text-muted">Lightning Engine p50</div>
			</div>
			
			<!-- Latency Distribution -->
			<div class="la-card" style="grid-column: span 2;">
				<div class="la-label">Latency Distribution</div>
				<table class="la-table">
					<tr><td>⚡ Ultra Fast (&lt; 10ms)</td><td align="right"><b>${data.latency.sub_10 || 0}</b></td></tr>
					<tr><td>🐇 Fast (10 - 50ms)</td><td align="right"><b>${data.latency.sub_50 || 0}</b></td></tr>
					<tr><td>🚶 Normal (50 - 100ms)</td><td align="right"><b>${data.latency.sub_100 || 0}</b></td></tr>
					<tr><td>🐢 Slow (&gt; 100ms)</td><td align="right"><b>${data.latency.over_100 || 0}</b></td></tr>
				</table>
			</div>

			<!-- Zero Results -->
			<div class="la-card">
				<div class="la-label">Top Zero-Result Queries</div>
				<table class="la-table">
					<tr><th>Query</th><th align="right">Count</th></tr>
					${data.zero_results.map(z => `<tr><td>"${z.query}"</td><td align="right"><span class="la-pill">${z.count}</span></td></tr>`).join('')}
					${data.zero_results.length === 0 ? '<tr><td colspan="2" class="text-muted">No zero-result queries recorded.</td></tr>' : ''}
				</table>
			</div>

			<!-- Popular Clicks -->
			<div class="la-card">
				<div class="la-label">Most Clicked Results</div>
				<table class="la-table">
					<tr><th>Document</th><th align="right">Clicks</th></tr>
					${data.top_targets.map(t => `<tr><td><b>${t.clicked_doctype}</b>: <a href="/app/${frappe.router.slug(t.clicked_doctype)}/${t.clicked_name}">${t.clicked_name}</a></td><td align="right"><span class="la-pill">${t.count}</span></td></tr>`).join('')}
					${data.top_targets.length === 0 ? '<tr><td colspan="2" class="text-muted">No click signals recorded yet.</td></tr>' : ''}
				</table>
			</div>
		</div>
	`;

	$(page.main).html(html);
}