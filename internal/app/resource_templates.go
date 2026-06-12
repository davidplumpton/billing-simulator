package app

var resourcePageTemplate = newPageTemplate("resource-page", `<div class="page-heading">
			<div>
				<h1>Resources</h1>
			</div>
		</div>

		<div id="resources-refresh" data-partial-surface="resources">
			{{template "resources.refresh" .}}
		</div>

{{define "resources.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Resource filters">
				<form method="get" action="/resources" class="filter-form" data-partial-form="resources" data-partial-target="#resources-refresh" data-partial-auto="true">
					<label>Account ID
						<input name="account_id" value="{{.Filters.AccountID}}">
					</label>
					<label>Service
						<input name="service_code" value="{{.Filters.ServiceCode}}">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
				<form method="post" action="/clock/advance" class="clock-form">
					{{template "ui.input-field" .ClockAmountField}}
					{{template "ui.select-field" .ClockUnitField}}
					{{template "ui.submit-button" .ClockSubmitButton}}
				</form>
			</section>

			<section class="form-grid">
				<form method="post" action="/resources/create" class="panel">
					<h2>Create Resource</h2>
					<div class="fields">
						<label>Account ID
							<input name="account_id" value="{{.DefaultAccountID}}" required>
						</label>
						<label>Region
							<select name="region_code">
								{{range .RegionOptions}}<option value="{{.}}">{{.}}</option>{{end}}
							</select>
						</label>
						<label>Service
							<select name="service_preset">
								{{range .ResourcePresets}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Size
							<input name="size" value="t3.medium" required>
						</label>
						<label>Name
							<input name="resource_name" value="Storefront web">
						</label>
						<label>Lifecycle
							<select name="status">
								{{range .StatusOptions}}<option value="{{.}}">{{.}}</option>{{end}}
							</select>
						</label>
						<label>Started At
							<input type="datetime-local" name="started_at" value="{{.DefaultUsageStart}}">
						</label>
						<label class="wide">Tags
							<textarea name="tags" rows="3">app=storefront
owner=web-platform</textarea>
						</label>
					</div>
					<button type="submit">Create Resource</button>
				</form>

				<form method="post" action="/resources/usage" class="panel">
					<h2>Generate Usage</h2>
					<div class="fields">
						<label>Resource
							<select name="resource_id" required>
								{{range .Resources}}<option value="{{.ID}}">{{.Name}} - {{.ServiceCode}}</option>{{end}}
							</select>
						</label>
						<label>Usage
							<select name="usage_preset">
								{{range .UsagePresets}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Quantity
							<input name="quantity" value="1" inputmode="decimal" required>
						</label>
						<label>Start
							<input type="datetime-local" name="usage_start_time" value="{{.DefaultUsageStart}}">
						</label>
						<label>End
							<input type="datetime-local" name="usage_end_time" value="{{.DefaultUsageEnd}}">
						</label>
					</div>
					<button type="submit">Generate Usage</button>
				</form>

				<form method="post" action="/resources/generate" class="panel compact">
					<h2>Generate Pattern</h2>
					<div class="fields">
						<label>Resource
							<select name="resource_id" required>
								{{range .Resources}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
							</select>
						</label>
						<label>Pattern
							<select name="generation_pattern">
								{{range .UsageGenerationPresets}}<option value="{{.Key}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Start Date
							<input type="date" name="generation_start_date" value="{{.DefaultGenerationStartDate}}">
						</label>
						<label>Days
							<input name="generation_days" value="{{.DefaultGenerationDays}}" inputmode="numeric" required>
						</label>
					</div>
					<button type="submit">Generate Pattern</button>
				</form>

				<form method="post" action="/resources/tags" class="panel compact">
					<h2>Add Tag</h2>
					<div class="fields">
						<label>Resource
							<select name="resource_id" required>
								{{range .Resources}}<option value="{{.ID}}">{{.Name}}</option>{{end}}
							</select>
						</label>
						<label>Key
							<input name="tag_key" required>
						</label>
						<label>Value
							<input name="tag_value">
						</label>
					</div>
					<button type="submit">Add Tag</button>
				</form>

				<form method="post" action="/resources/billing-pipeline" class="panel compact">
					<h2>Price Usage</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccountID}}">
						</label>
					</div>
					<button type="submit">Run Billing Pipeline</button>
				</form>

				<form method="post" action="/resources/daily-metering" class="panel compact">
					<h2>Daily Metering</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccountID}}">
						</label>
					</div>
					<button type="submit">Run Daily Metering</button>
				</form>

				<form method="post" action="/resources/month-close" class="panel compact">
					<h2>Month-End Close</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccountID}}">
						</label>
						<label>Invoice Due Days
							<input name="invoice_due_days" value="14" inputmode="numeric" required>
						</label>
					</div>
					<button type="submit">Close Previous Period</button>
				</form>
			</section>

			<section>
				<div class="section-heading">
					<h2>Inventory</h2>
					<span>{{len .Resources}} resources</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Inventory}}
						<tbody>
							{{range .Resources}}
								<tr>
									<td><strong>{{.Name}}</strong><small>{{.ResourceType}}</small></td>
									<td>{{.AccountID}}</td>
									<td>{{.ServiceCode}}</td>
									<td>{{.RegionCode}}</td>
									<td>{{.Size}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{template "tags" .Tags}}</td>
									<td>{{.UsageEventCount}}{{if .LastUsageEndTime}}<small>{{.LastUsageEndTime}}</small>{{end}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Inventory}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Recent Usage</h2>
					<span>{{len .UsageEvents}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.RecentUsage}}
						<tbody>
							{{range .UsageEvents}}
								<tr>
									<td><strong>{{.ResourceName}}</strong><small>{{.AccountID}}</small></td>
									<td><code>{{.BillableDimensions}}</code></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}} {{.Unit}}</td>
									<td>{{.EstimatedCost}}</td>
									<td>{{template "tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.RecentUsage}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Current Billing Summary</h2>
					<span>{{len .BillingPeriodSummaries}} summaries</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillingPeriodSummaries}}
						<tbody>
							{{range .BillingPeriodSummaries}}
								<tr>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.UsageAccountID}}</td>
									<td>{{.ServiceCode}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.Cost}}</td>
									<td>{{.RefreshedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillingPeriodSummaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Daily Metering Jobs</h2>
					<span>{{len .DailyMeteringJobRuns}} runs</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.DailyMeteringJobRuns}}
						<tbody>
							{{range .DailyMeteringJobRuns}}
								<tr>
									<td><strong>{{.CompletedAt}}</strong><small>{{.ID}}</small></td>
									<td>{{.Trigger}}</td>
									<td>{{.ClockTime}}</td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.MeteringRecordsCreated}}</td>
									<td>{{.BillLineItemsCreated}}</td>
									<td>{{.SummariesRefreshed}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.DailyMeteringJobRuns}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Closed Billing Periods</h2>
					<span>{{len .MonthEndCloses}} closes</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.MonthEndCloses}}
						<tbody>
							{{range .MonthEndCloses}}
								<tr>
									<td><strong>{{.ClosedAt}}</strong><small>{{.ID}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.MeteringRecordsCreated}}</td>
									<td>{{.FinalizedLineItems}}<small>{{.BillLineItemsCreated}} new</small></td>
									<td>{{.FinalizedCost}}</td>
									<td>{{.SummariesRefreshed}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.MonthEndCloses}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Issued Bills</h2>
					<span>{{len .IssuedBills}} bills</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.IssuedBills}}
						<tbody>
							{{range .IssuedBills}}
								<tr>
									<td><strong>{{.ID}}</strong><small>{{.InvoiceID}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><span class="status">{{.BillState}}</span></td>
									<td>{{.LineItemCount}}</td>
									<td>{{.UsageCharge}}<small>Credits {{.Credits}} / refunds {{.Refunds}}</small></td>
									<td>{{.Tax}}</td>
									<td><strong>{{.Total}}</strong></td>
									<td><span class="status">{{.InvoiceStatus}}</span><small>{{.InvoiceAmountDue}}</small></td>
									<td>{{.InvoiceDueDate}}<small>{{.InvoiceDate}}</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.IssuedBills}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Metering Records</h2>
					<span>{{len .MeteringRecords}} records</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.MeteringRecords}}
						<tbody>
							{{range .MeteringRecords}}
								<tr>
									<td><strong>{{.ResourceName}}</strong><small>{{.AccountID}}</small></td>
									<td><code>{{.BillableDimensions}}</code></td>
									<td>{{.Window}}</td>
									<td>{{.Quantity}} {{.Unit}}</td>
									<td>{{template "tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.MeteringRecords}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Bill Line Items</h2>
					<span>{{len .BillLineItems}} items</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillLineItems}}
						<tbody>
							{{range .BillLineItems}}
								<tr>
									<td><strong>{{.ResourceName}}</strong><small>{{.PriceCatalogSKU}} @ {{.PriceEffectiveOn}}</small></td>
									<td>{{.Period}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td><strong>{{.PayerAccountID}}</strong><small>{{.UsageAccountID}}</small></td>
									<td>{{.ServiceCode}}</td>
									<td>{{.Description}}</td>
									<td>{{.PricingQuantity}} {{.PricingUnit}}</td>
									<td>{{.UnblendedRate}}</td>
									<td>{{.UnblendedCost}}</td>
									<td>{{template "tags" .Tags}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillLineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Price Dimensions</h2>
					<span>{{len .CatalogItems}} rates</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PriceDimensions}}
						<tbody>
							{{range .CatalogItems}}
								<tr>
									<td>{{.ServiceCode}}</td>
									<td><code>{{.BillableDimensions}}</code></td>
									<td>{{.Unit}}</td>
									<td>{{.UnitRate}}</td>
									<td>{{.PeriodEstimate}}</td>
								</tr>
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}

{{define "tags"}}
	{{if .}}
		<div class="tags">
			{{range .}}<span>{{.Key}}={{.Value}}</span>{{end}}
		</div>
	{{else}}
		<span class="muted">untagged</span>
	{{end}}
{{end}}
`)
