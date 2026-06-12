package app

var exportsPageTemplate = newPageTemplate("exports-page", `<div class="page-heading">
			<div>
				<h1>Exports</h1>
			</div>
			{{template "ui.action-bar" .Actions}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Export file filters">
				<form method="get" action="/exports" class="filter-form">
					{{template "ui.select-field" .Filters.ExportTypeField}}
					{{template "ui.select-field" .Filters.ViewerRoleField}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.Filters.BillingPeriodStart}}" placeholder="2026-02-01">
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.Filters.BillingPeriodEnd}}" placeholder="2026-03-01">
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.Filters.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.Filters.UsageAccountID}}">
					</label>
					<label>Limit
						<input name="limit" value="{{.Filters.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			<section class="filter-bar" aria-label="Generate CUR CSV export">
				<form method="post" action="/exports/generate-cur" class="filter-form">
					{{template "ui.select-field" .GenerateCURCSV.ViewerRoleField}}
					{{template "ui.input-field" .GenerateCURCSV.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.GenerateCURCSV.BillingPeriodStart}}" placeholder="2026-02-01" required>
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.GenerateCURCSV.BillingPeriodEnd}}" placeholder="2026-03-01" required>
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.GenerateCURCSV.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.GenerateCURCSV.UsageAccountID}}">
					</label>
					{{template "ui.select-field" .GenerateCURCSV.LineItemStatusField}}
					<label>Limit
						<input name="limit" value="{{.GenerateCURCSV.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .GenerateCURCSV.GenerateButton}}
				</form>
			</section>

			<section class="filter-bar" aria-label="Generate FOCUS CSV export">
				<form method="post" action="/exports/generate-focus" class="filter-form">
					{{template "ui.select-field" .GenerateFOCUSCSV.ViewerRoleField}}
					{{template "ui.input-field" .GenerateFOCUSCSV.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.GenerateFOCUSCSV.BillingPeriodStart}}" placeholder="2026-02-01" required>
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.GenerateFOCUSCSV.BillingPeriodEnd}}" placeholder="2026-03-01" required>
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.GenerateFOCUSCSV.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.GenerateFOCUSCSV.UsageAccountID}}">
					</label>
					{{template "ui.select-field" .GenerateFOCUSCSV.LineItemStatusField}}
					<label>Limit
						<input name="limit" value="{{.GenerateFOCUSCSV.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .GenerateFOCUSCSV.GenerateButton}}
				</form>
			</section>

			<section>
				<div class="section-heading">
					<h2>Generated Exports</h2>
					<span>{{len .Files}} files, recently updated first</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table exports-table">
						{{template "ui.dense-table-head" .Tables.Files}}
						<tbody>
							{{range .Files}}
								<tr>
									<td><strong>{{.Filename}}</strong><small>created {{.CreatedAt}}</small></td>
									<td><span class="status">{{.ExportType}}</span></td>
									<td>{{.Period}}</td>
									<td><strong>payer {{.PayerAccountID}}</strong><small>usage {{.UsageAccountID}}</small><small>{{.LineItemStatus}}</small></td>
									<td><strong>bill {{.SourceBillID}}</strong><small>generated {{.GeneratedAt}}</small><small>{{.RowsWritten}} rows</small></td>
									<td>{{.Size}}</td>
									<td><code>{{.Checksum}}</code></td>
									<td>{{.UpdatedAt}}</td>
									<td class="actions-cell">
										<div class="inline-actions compact-actions">
											<a class="button-link secondary" href="{{.DownloadPath}}">Download</a>
											{{if .ReconciliationPath}}<a class="button-link secondary" href="{{.ReconciliationPath}}">Reconcile</a>{{end}}
											{{if .CanRegenerate}}
												<form method="post" action="/exports/regenerate">
													<input type="hidden" name="filename" value="{{.RegenerateFilename}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button type="submit">Regenerate</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Files}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)

var exportReconciliationPageTemplate = newPageTemplate("export-reconciliation-page", `<div class="page-heading">
			<div>
				<h1>Export Reconciliation</h1>
			</div>
			{{template "ui.action-bar" .Actions}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="filter-bar" aria-label="Export reconciliation filters">
				<form method="get" action="/exports/reconciliation" class="filter-form">
					{{template "ui.select-field" .Filters.ViewerRoleField}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
					<label>Billing Period Start
						<input name="billing_period_start" value="{{.Filters.BillingPeriodStart}}" placeholder="2026-02-01">
					</label>
					<label>Billing Period End
						<input name="billing_period_end" value="{{.Filters.BillingPeriodEnd}}" placeholder="2026-03-01">
					</label>
					<label>Payer Account ID
						<input name="payer_account_id" value="{{.Filters.PayerAccountID}}">
					</label>
					<label>Usage Account ID
						<input name="usage_account_id" value="{{.Filters.UsageAccountID}}">
					</label>
					{{template "ui.select-field" .Filters.LineItemStatusField}}
					<label>Limit
						<input name="limit" value="{{.Filters.Limit}}" inputmode="numeric">
					</label>
					{{template "ui.submit-button" .Filters.ApplyButton}}
					{{if .Filters.HasFilters}}<a class="button-link secondary" href="{{.Filters.ClearPath}}">Clear</a>{{end}}
				</form>
			</section>

			{{if .Loaded}}
				<section class="clock-strip">
					<div>
						<h2>Report Status</h2>
						<strong>{{.Report.Status}}</strong>
						<small>{{.Report.Flags}}</small>
					</div>
					<div class="detail-list">
						<span>Export Selection</span>
						<strong>{{.Report.Period}}</strong>
						<small>{{.Report.CurrencyCode}} payer {{.Report.PayerAccountID}}</small>
						<small>{{.Report.UsageAccountID}} - {{.Report.LineItemStatus}}</small>
					</div>
				</section>

				<section>
					<div class="section-heading">
						<h2>Bill and Invoice Comparison</h2>
						<span>{{len .Report.DocumentRows}} sources</span>
					</div>
					<div class="table-wrap">
						<table class="dense-table">
							{{template "ui.dense-table-head" .Tables.Documents}}
							<tbody>
								{{range .Report.DocumentRows}}
									<tr>
										<td><strong>{{.Source}}</strong></td>
										<td>{{.ID}}</td>
										<td><span class="status">{{.Status}}</span></td>
										<td>{{.LineItemCount}}</td>
										<td>{{.Charges}}</td>
										<td>{{.Credits}}</td>
										<td>{{.Refunds}}</td>
										<td>{{.Tax}}</td>
										<td><strong>{{.Total}}</strong></td>
										<td>{{.ItemResidual}}</td>
										<td>{{.ChargeResidual}}</td>
										<td>{{.CreditResidual}}</td>
										<td>{{.RefundResidual}}</td>
										<td>{{.TaxResidual}}</td>
										<td><strong>{{.TotalResidual}}</strong></td>
									</tr>
								{{else}}
									{{template "ui.dense-table-empty-row" $.Tables.Documents}}
								{{end}}
							</tbody>
						</table>
					</div>
				</section>
			{{end}}
		{{end}}
`)
