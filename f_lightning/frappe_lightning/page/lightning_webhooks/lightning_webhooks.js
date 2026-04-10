frappe.pages['lightning-webhooks'].on_page_load = function(wrapper) {
	var page = frappe.ui.make_app_page({
		parent: wrapper,
		title: '⚡ Webhook Dashboard',
		single_column: true
	});

	// Refresh button
	page.add_button(__('Refresh'), function() {
		load_dashboard(page, wrapper);
	}, { icon: 'refresh' });

	page.set_indicator('Loading...', 'orange');

	$(wrapper).bind('show', function() {
		load_dashboard(page, wrapper);
	});
};

function load_dashboard(page, wrapper) {
	page.set_indicator('Fetching...', 'orange');
	frappe.call({
		method: 'f_lightning.frappe_lightning.api.get_webhook_dashboard_data',
		callback: function(r) {
			if (r.message) {
				page.set_indicator('Live', 'green');
				render_dashboard(page, wrapper, r.message);
			} else {
				page.set_indicator('No data', 'grey');
			}
		},
		error: function() {
			page.set_indicator('Error', 'red');
		}
	});
}

function render_dashboard(page, wrapper, data) {
	var stats = data.stats || {};
	var subs  = data.subscriptions || [];
	var logs  = data.recent_logs || [];
	var retry = data.retry_breakdown || [];
	var trend = data.hourly_trend || [];

	var successRate = stats.success_rate != null ? stats.success_rate + '%' : '—';
	var successColor = (parseFloat(stats.success_rate) >= 95) ? 'var(--green-500)'
		: (parseFloat(stats.success_rate) >= 80) ? 'var(--yellow-500)' : 'var(--red-500)';

	var retryRows = retry.map(function(r) {
		return '<tr><td>Attempt #' + r.attempt_num + '</td>'
			+ '<td align="right">' + r.count + '</td></tr>';
	}).join('') || '<tr><td colspan="2" class="text-muted">No retries in last 24h</td></tr>';

	var trendRows = trend.map(function(t) {
		var rate = t.deliveries > 0 ? Math.round(t.succeeded / t.deliveries * 100) : 0;
		var bar = '<div style="background:var(--green-500);height:6px;width:' + rate + '%;border-radius:3px"></div>';
		return '<tr><td style="font-family:monospace">' + t.hour + '</td>'
			+ '<td align="right">' + t.deliveries + '</td>'
			+ '<td align="right">' + t.succeeded + '</td>'
			+ '<td align="right" style="color:var(--red-500)">' + t.failed + '</td>'
			+ '<td style="min-width:80px">' + bar + '</td></tr>';
	}).join('') || '<tr><td colspan="5" class="text-muted">No deliveries in last 24h</td></tr>';

	var subRows = subs.map(function(s) {
		var badge = s.enabled
			? '<span class="lw-badge lw-badge-green">enabled</span>'
			: '<span class="lw-badge lw-badge-grey">disabled</span>';
		var rate = s.total_deliveries > 0
			? Math.round(s.successful / s.total_deliveries * 100) + '%' : '—';
		return '<tr>'
			+ '<td><b>' + s.name + '</b><br>'
			+ '<small class="text-muted">' + (s.doctype_filter || '*') + '</small></td>'
			+ '<td style="max-width:200px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">'
			+   '<small>' + (s.endpoint_url || '') + '</small></td>'
			+ '<td>' + badge + '</td>'
			+ '<td align="right">' + (s.total_deliveries || 0) + '</td>'
			+ '<td align="right" style="color:var(--green-500)">' + (s.successful || 0) + '</td>'
			+ '<td align="right" style="color:var(--red-500)">' + (s.failed || 0) + '</td>'
			+ '<td align="right">' + rate + '</td>'
			+ '<td align="right">' + (s.avg_latency_ms || '—') + 'ms</td>'
			+ '</tr>';
	}).join('') || '<tr><td colspan="8" class="text-muted">No subscriptions configured.</td></tr>';

	var logRows = logs.map(function(l) {
		var icon   = l.success ? '✓' : '✗';
		var color  = l.success ? 'var(--green-500)' : 'var(--red-500)';
		var replayBtn = !l.success
			? '<button class="btn btn-xs btn-default lw-replay" data-id="' + l.delivery_id + '">Replay</button>'
			: '';
		return '<tr>'
			+ '<td style="font-family:monospace;font-size:0.75rem">'
			+   l.delivery_id.substring(0, 8) + '…</td>'
			+ '<td>' + (l.subscription || '—') + '</td>'
			+ '<td>' + (l.doctype_name || '—') + '</td>'
			+ '<td><code>' + (l.event || '') + '</code></td>'
			+ '<td align="right"><b style="color:' + color + '">' + icon + '</b> '
			+   (l.status_code || '—') + '</td>'
			+ '<td align="right">' + (l.latency_ms || '—') + 'ms</td>'
			+ '<td align="right">' + (l.attempt_num || 1) + '</td>'
			+ '<td>' + (l.error
				? '<small style="color:var(--red-500)" title="' + frappe.utils.escape_html(l.error || '') + '">'
				+   (l.error || '').substring(0, 40) + (l.error && l.error.length > 40 ? '…' : '') + '</small>'
				: '') + replayBtn + '</td>'
			+ '</tr>';
	}).join('') || '<tr><td colspan="8" class="text-muted">No deliveries logged yet.</td></tr>';

	var html = `
		<style>
			.lw-wrap  { padding: 16px 20px; }
			.lw-kpi   { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 14px; margin-bottom: 20px; }
			.lw-card  { background: var(--card-bg); border: 1px solid var(--border-color);
			            border-radius: 8px; padding: 18px; box-shadow: var(--shadow-sm); }
			.lw-card h5 { text-transform: uppercase; font-size: 0.72rem; letter-spacing: .06em;
			              color: var(--text-muted); margin: 0 0 8px; font-weight: 600; }
			.lw-big   { font-size: 2rem; font-weight: 700; line-height: 1; color: var(--text-color); }
			.lw-sub   { font-size: 0.8rem; color: var(--text-muted); margin-top: 4px; }
			.lw-section { margin-bottom: 20px; }
			.lw-section h4 { font-size: 0.9rem; font-weight: 600; color: var(--text-muted);
			                 text-transform: uppercase; letter-spacing: .05em; margin-bottom: 10px; }
			.lw-table { width: 100%; border-collapse: collapse; font-size: 0.875rem; }
			.lw-table th { text-align: left; padding: 8px 10px; border-bottom: 2px solid var(--border-color);
			               color: var(--text-muted); font-weight: 500; font-size: 0.8rem;
			               text-transform: uppercase; letter-spacing: .04em; }
			.lw-table td { padding: 8px 10px; border-bottom: 1px solid var(--border-color);
			               vertical-align: middle; }
			.lw-table tr:last-child td { border-bottom: none; }
			.lw-table tr:hover td { background: var(--fg-color); }
			.lw-badge { display: inline-block; padding: 2px 8px; border-radius: 10px;
			            font-size: 0.75rem; font-weight: 500; }
			.lw-badge-green { background: rgba(34,197,94,.15); color: var(--green-500); }
			.lw-badge-grey  { background: var(--bg-color); color: var(--text-muted); }
			.lw-2col { display: grid; grid-template-columns: 1fr 1fr; gap: 14px; }
			@media (max-width: 900px) { .lw-2col { grid-template-columns: 1fr; } }
		</style>

		<div class="lw-wrap">

			<!-- KPI strip -->
			<div class="lw-kpi">
				<div class="lw-card">
					<h5>Deliveries (24h)</h5>
					<div class="lw-big">${stats.total || 0}</div>
					<div class="lw-sub">total attempts</div>
				</div>
				<div class="lw-card">
					<h5>Success Rate</h5>
					<div class="lw-big" style="color:${successColor}">${successRate}</div>
					<div class="lw-sub">${stats.successful || 0} succeeded</div>
				</div>
				<div class="lw-card">
					<h5>Failed</h5>
					<div class="lw-big" style="color:var(--red-500)">${stats.failed || 0}</div>
					<div class="lw-sub">moved to DLQ if exhausted</div>
				</div>
				<div class="lw-card">
					<h5>Retried</h5>
					<div class="lw-big">${stats.retried || 0}</div>
					<div class="lw-sub">attempt > 1</div>
				</div>
				<div class="lw-card">
					<h5>Avg Latency</h5>
					<div class="lw-big">${stats.avg_latency_ms || '—'}<span style="font-size:1rem;color:var(--text-muted)">ms</span></div>
					<div class="lw-sub">end-to-end HTTP</div>
				</div>
				<div class="lw-card">
					<h5>Subscriptions</h5>
					<div class="lw-big">${subs.filter(s=>s.enabled).length}<span style="font-size:1rem;color:var(--text-muted)"> / ${subs.length}</span></div>
					<div class="lw-sub">active / total</div>
				</div>
			</div>

			<!-- 2-column: trend + retry breakdown -->
			<div class="lw-2col lw-section">
				<div class="lw-card">
					<h4>Hourly Delivery Trend (24h)</h4>
					<table class="lw-table">
						<thead><tr>
							<th>Hour</th><th align="right">Total</th>
							<th align="right">OK</th><th align="right">Fail</th>
							<th>Rate</th>
						</tr></thead>
						<tbody>${trendRows}</tbody>
					</table>
				</div>
				<div class="lw-card">
					<h4>Retry Breakdown (24h)</h4>
					<table class="lw-table">
						<thead><tr><th>Attempt</th><th align="right">Count</th></tr></thead>
						<tbody>${retryRows}</tbody>
					</table>
				</div>
			</div>

			<!-- Subscriptions table -->
			<div class="lw-card lw-section">
				<h4>Webhook Subscriptions</h4>
				<table class="lw-table">
					<thead><tr>
						<th>Name</th><th>Endpoint</th><th>Status</th>
						<th align="right">Total</th><th align="right">OK</th>
						<th align="right">Fail</th><th align="right">Rate</th>
						<th align="right">Avg Latency</th>
					</tr></thead>
					<tbody>${subRows}</tbody>
				</table>
			</div>

			<!-- Recent delivery log -->
			<div class="lw-card lw-section">
				<h4>Recent Deliveries (last 50)</h4>
				<table class="lw-table">
					<thead><tr>
						<th>Delivery ID</th><th>Subscription</th><th>DocType</th>
						<th>Event</th><th align="right">Status</th>
						<th align="right">Latency</th><th align="right">Attempt</th>
						<th>Details</th>
					</tr></thead>
					<tbody id="lw-log-body">${logRows}</tbody>
				</table>
			</div>

		</div>`;

	$(wrapper).find('.page-content').empty().html(html);

	// Replay button handler
	$(wrapper).find('.lw-replay').on('click', function() {
		var id = $(this).data('id');
		var btn = $(this);
		btn.prop('disabled', true).text('Queuing…');
		frappe.call({
			method: 'f_lightning.frappe_lightning.api.replay_webhook_delivery',
			args: { delivery_id: id },
			callback: function(r) {
				if (r.message && r.message.status === 'queued') {
					frappe.show_alert({ message: 'Delivery queued for replay', indicator: 'green' });
					btn.text('Queued ✓').addClass('btn-success');
				} else {
					btn.prop('disabled', false).text('Replay');
					frappe.show_alert({ message: 'Replay failed', indicator: 'red' });
				}
			},
			error: function() {
				btn.prop('disabled', false).text('Replay');
			}
		});
	});
}
