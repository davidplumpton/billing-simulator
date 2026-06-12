package app

var costExplorerPageTemplate = newPageTemplate("cost-explorer-page", `<div class="page-heading">
			<div>
				<h1>Cost Explorer</h1>
			</div>
		</div>

		<div id="cost-explorer-refresh" data-partial-surface="cost-explorer">
			{{template "cost-explorer.refresh" .}}
		</div>

{{define "cost-explorer.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="report-toolbar">
				<form method="get" action="/cost-explorer" class="saved-report-picker">
					<input type="hidden" name="owner_account_id" value="{{.Builder.OwnerAccountID}}">
					<input type="hidden" name="owner_role" value="{{.Builder.OwnerRole}}">
					<label>Saved Report
						<select name="saved_report_id">
							<option value="">Custom report</option>
							{{range .SavedReports}}<option value="{{.ID}}"{{if .Selected}} selected{{end}}>{{.Name}}</option>{{end}}
						</select>
					</label>
					<button type="submit">Load Report</button>
				</form>
				<div class="page-actions">
					<a class="button-link secondary" href="/resources">Resources</a>
					<a class="button-link secondary" href="/cost-categories">Cost Categories</a>
					<a class="button-link" href="{{.NewReportPath}}">New Report</a>
				</div>
			</section>

			<form method="get" action="/cost-explorer" class="report-builder-form">
				<input type="hidden" name="saved_report_id" value="{{.Builder.SavedReportID}}">
				<div class="builder-grid">
					<section class="panel builder-panel">
						<h2>Report Definition</h2>
						<div class="fields">
							<label class="form-row">Name
								<input name="report_name" value="{{.Builder.ReportName}}">
							</label>
							<label class="form-row">Owner Account
								<input name="owner_account_id" value="{{.Builder.OwnerAccountID}}" required>
							</label>
							<label class="form-row">Owner Role
								<select name="owner_role" required>
									{{range .OwnerRoleOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Description
								<input name="description" value="{{.Builder.Description}}">
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Time and Metric</h2>
						<div class="fields">
							<label class="form-row">Start Date
								<input type="date" name="date_range_start" value="{{.Builder.DateRangeStart}}" required>
							</label>
							<label class="form-row">End Date
								<input type="date" name="date_range_end" value="{{.Builder.DateRangeEnd}}" required>
							</label>
							<label class="form-row">Granularity
								<select name="granularity" required>
									{{range .GranularityOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Metric
								<select name="metric" required>
									{{range .MetricOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Chart
								<select name="chart_type" required>
									{{range .ChartTypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Filters</h2>
						<div class="fields">
							<label class="form-row">Service Values
								<input name="service_values" value="{{.Builder.ServiceValues}}">
							</label>
							<label class="form-row">Linked Accounts
								<input name="linked_account_values" value="{{.Builder.LinkedAccountValues}}">
							</label>
							<label class="form-row">Regions
								<input name="region_values" value="{{.Builder.RegionValues}}">
							</label>
							<label class="form-row">Usage Types
								<input name="usage_type_values" value="{{.Builder.UsageTypeValues}}">
							</label>
							<label class="form-row">Line Item Types
								<input name="line_item_type_values" value="{{.Builder.LineItemTypeValues}}">
							</label>
							<label class="form-row">Tag Key
								<input name="tag_key" value="{{.Builder.TagKey}}" list="cost-explorer-group-keys">
							</label>
							<label class="form-row">Tag Values
								<input name="tag_values" value="{{.Builder.TagValues}}">
							</label>
							<label class="form-row">Cost Category
								<input name="cost_category_key" value="{{.Builder.CostCategoryKey}}" list="cost-explorer-group-keys">
							</label>
							<label class="form-row">Category Values
								<input name="cost_category_values" value="{{.Builder.CostCategoryValues}}">
							</label>
						</div>
					</section>

					<section class="panel builder-panel">
						<h2>Group By</h2>
						<div class="fields">
							<label class="form-row">Group 1 Type
								<select name="group_1_type">
									{{range .Group1TypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Group 1 Key
								<input name="group_1_key" value="{{.Builder.Group1Key}}" list="cost-explorer-group-keys">
							</label>
							<label class="form-row">Group 2 Type
								<select name="group_2_type">
									{{range .Group2TypeOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
								</select>
							</label>
							<label class="form-row">Group 2 Key
								<input name="group_2_key" value="{{.Builder.Group2Key}}" list="cost-explorer-group-keys">
							</label>
						</div>
						<div class="form-actions">
							<button type="submit" name="run" value="1">Run Report</button>
							<button type="submit" formmethod="post" formaction="/cost-explorer/reports/save">Save Report</button>
						</div>
					</section>
				</div>
				<datalist id="cost-explorer-group-keys">
					{{range .GroupKeyOptions}}<option value="{{.}}"></option>{{end}}
				</datalist>
			</form>

			<section class="state-grid" aria-label="Cost Explorer totals">
				{{range .Result.StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Report Results</h2>
					<span>{{.Result.MetricLabel}} / {{.Result.Granularity}} / {{.Result.ChartType}}</span>
					{{if .Result.CSVPath}}<a class="button-link secondary" href="{{.Result.CSVPath}}">CSV</a>{{end}}
				</div>
				{{if .Result.Chart.HasChart}}
					<div class="report-chart-panel" aria-label="Report chart">
						<div class="report-chart-heading">
							<div>
								<strong>{{.Result.Chart.MetricLabel}}</strong>
								<small>{{.Result.DateRangeStart}} to {{.Result.DateRangeEnd}} - {{.Result.Chart.YAxisLabel}}</small>
							</div>
							<div class="chart-legend">
								{{range .Result.Chart.Legend}}
									<span><i style="background: {{.Color}}"></i>{{.Label}}</span>
								{{end}}
							</div>
						</div>
						<svg class="report-chart report-chart-{{.Result.Chart.Type}}" viewBox="0 0 {{.Result.Chart.Width}} {{.Result.Chart.Height}}" role="img" aria-labelledby="cost-explorer-chart-title">
							<title id="cost-explorer-chart-title">{{.Result.Chart.Title}}</title>
							<rect class="chart-plot" x="{{.Result.Chart.PlotX}}" y="{{.Result.Chart.PlotY}}" width="{{.Result.Chart.PlotWidth}}" height="{{.Result.Chart.PlotHeight}}"></rect>
							{{range .Result.Chart.Ticks}}
								<line class="chart-gridline" x1="58" y1="{{.Y}}" x2="708" y2="{{.Y}}"></line>
								<text class="chart-y-label" x="48" y="{{.Y}}">{{.Label}}</text>
							{{end}}
							<line class="chart-axis" x1="58" y1="{{.Result.Chart.ZeroY}}" x2="708" y2="{{.Result.Chart.ZeroY}}"></line>
							{{range .Result.Chart.Bars}}
								<rect class="chart-bar" x="{{.X}}" y="{{.Y}}" width="{{.Width}}" height="{{.Height}}" fill="{{.Color}}">
									<title>{{.Period}} - {{.Label}} - {{.ValueLabel}}</title>
								</rect>
							{{end}}
							{{range .Result.Chart.Lines}}
								<polyline class="chart-line" points="{{.Points}}" stroke="{{.Color}}"></polyline>
								{{$lineColor := .Color}}
								{{range .Nodes}}
									<circle class="chart-point" cx="{{.X}}" cy="{{.Y}}" r="3.5" fill="{{$lineColor}}">
										<title>{{.Period}} - {{.Label}} - {{.ValueLabel}}</title>
									</circle>
								{{end}}
							{{end}}
							{{range .Result.Chart.XLabels}}
								<text class="chart-x-label" x="{{.X}}" y="252">{{.Label}}</text>
							{{end}}
						</svg>
					</div>
				{{end}}
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Results}}
						<tbody>
							{{range .Result.Rows}}
								<tr>
									<td>{{.PeriodStart}}</td>
									<td>{{.PeriodEnd}}</td>
									<td><span class="status">{{.Group1}}</span></td>
									<td><span class="status">{{.Group2}}</span></td>
									<td><strong>{{.MetricValue}}</strong></td>
									<td>{{.Usage}}</td>
									<td>{{.Cost}}</td>
									<td>{{.LineItems}}</td>
									<td>{{.CurrencyCode}}</td>
									<td><a class="button-link secondary" href="{{.DrilldownPath}}">Line Items</a></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Results}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Saved Reports</h2>
					<span>{{len .SavedReports}} reports</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.SavedReports}}
						<tbody>
							{{range .SavedReports}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.Owner}}</td>
									<td>{{.DateRange}}</td>
									<td>{{.Granularity}}</td>
									<td>{{.Metric}}</td>
									<td>{{.ChartType}}</td>
									<td>{{.LastRun}}</td>
									<td>
										<div class="inline-actions compact-actions">
											{{if .Selected}}<span class="status">Loaded</span>{{else}}<a class="button-link secondary" href="{{.LoadPath}}">Load</a>{{end}}
											<form method="post" action="/cost-explorer/reports/run">
												<input type="hidden" name="saved_report_id" value="{{.ID}}">
												<input type="hidden" name="owner_account_id" value="{{.OwnerAccountID}}">
												<input type="hidden" name="owner_role" value="{{.OwnerRole}}">
												<button type="submit">Run</button>
											</form>
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.SavedReports}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)

var costExplorerLineItemsPageTemplate = newPageTemplate("cost-explorer-line-items-page", `<div class="page-heading">
			<div>
				<h1>Cost Explorer Bill Line Items</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="{{.BackPath}}">Report</a>
				{{if .CSVPath}}<a class="button-link secondary" href="{{.CSVPath}}">CSV</a>{{end}}
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="report-toolbar">
				<div>
					<strong>{{.Period}}</strong>
					{{range .Groups}}<small>{{.}}</small>{{end}}
				</div>
			</section>

			<section class="state-grid" aria-label="Cost Explorer bill line item totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Source Line Items</h2>
					<span>{{.LineItemsLabel}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.ID}}</strong><small>{{.LineItemType}} {{.Status}}</small></td>
									<td><strong>{{.Resource}}</strong>{{if .ResourceID}}<small>{{.ResourceID}}</small>{{end}}</td>
									<td>{{.Period}}</td>
									<td><strong>{{.PayerAccountID}}</strong><small>{{.UsageAccountID}}</small></td>
									<td><strong>{{.Service}}</strong><small>{{.ServiceCode}} {{.RegionCode}}</small></td>
									<td><code>{{.UsageType}}</code><small>{{.Operation}}</small><small>{{.Description}}</small></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}}</td>
									<td>{{.Rate}}</td>
									<td><strong>{{.Cost}}</strong><small>{{.CurrencyCode}}</small></td>
									<td>{{template "cost-explorer.tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}

{{define "cost-explorer.tags"}}
	{{if .}}
		<div class="tags">
			{{range .}}<span>{{.Key}}={{.Value}}</span>{{end}}
		</div>
	{{else}}
		<span class="muted">untagged</span>
	{{end}}
{{end}}
`)
