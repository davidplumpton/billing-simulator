package app

var savingsPlanPageTemplate = newPageTemplate("savings-plans-page", `<div class="page-heading">
			<div>
				<h1>Savings Plans</h1>
			</div>
		</div>

		<div id="savings-plans-refresh" data-partial-surface="savings-plans">
			{{template "savings-plans.refresh" .}}
		</div>

{{define "savings-plans.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Compute Commitment Coverage</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.BillingPeriod}}</small>
				</div>
				<div class="page-actions">
					<a class="button-link secondary" href="/resources">Resources</a>
					<a class="button-link secondary" href="/bills">Bills</a>
					<a class="button-link" href="/savings-plans">Refresh</a>
				</div>
			</section>

			<section class="state-grid">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Value}}</strong>
					</div>
				{{end}}
			</section>

			<section class="form-grid">
				<form method="post" action="/savings-plans/create" class="panel">
					<h2>Create Compute Savings Plan</h2>
					<div class="fields">
						<label>Payer Account ID
							<input name="payer_account_id" value="{{.DefaultPayerAccount}}" required>
						</label>
						<label>Owner Account
							<select name="owner_account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Usage Type
							<input name="reference_usage_type" value="instance-hours:t3.medium" required>
						</label>
						<label>Region
							<input name="region_code" value="us-east-1" required>
						</label>
						<label>Sharing
							<select name="sharing_scope" required>
								{{range .SharingScopeOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Hourly Commitment USD
							<input name="hourly_commitment_usd" value="0.10" inputmode="decimal" required>
						</label>
						<label>Upfront Fee USD
							<input name="upfront_fee_usd" value="0.00" inputmode="decimal">
						</label>
						<label>Term Start
							<input type="datetime-local" name="term_start_time" value="{{.DefaultTermStart}}" required>
						</label>
						<label>Term End
							<input type="datetime-local" name="term_end_time" value="{{.DefaultTermEnd}}" required>
						</label>
						<label class="wide">Description
							<input name="description" value="Shared compute Savings Plan">
						</label>
					</div>
					<button type="submit">Create Savings Plan</button>
				</form>
			</section>

			<section>
				<div class="section-heading">
					<h2>Purchases</h2>
					<span>{{len .Purchases}} plans</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Purchases}}
						<tbody>
							{{range .Purchases}}
								<tr>
									<td><strong>{{.ID}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.PlanType}} / {{.Scope}}</small></td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.OwnerAccountID}}</td>
									<td><code>{{.Reference}}</code><small>{{.PriceLineage}}</small></td>
									<td>{{.Term}}</td>
									<td><strong>{{.HourlyCommitment}}</strong><small>Upfront {{.UpfrontFee}}</small></td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.GeneratedRows}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Purchases}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Generated Rows and Covered Usage</h2>
					<span>{{len .GeneratedSources}} rows</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.GeneratedSources}}
						<tbody>
							{{range .GeneratedSources}}
								<tr>
									<td><strong>{{.SavingsPlanID}}</strong><small>{{.Kind}}</small></td>
									<td><strong>{{.GeneratedRow}}</strong><small>{{.GeneratedMeta}}</small></td>
									<td>{{.GeneratedCost}}</td>
									<td>
										{{if .SourceAvailable}}
											<strong>{{.CoveredSource}}</strong><small>{{.CoveredMeta}}</small>
										{{else}}
											<span class="muted">Commitment fee</span>
										{{end}}
									</td>
									<td>{{.CoveredCost}}</td>
									<td>{{.AmortizedCost}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.GeneratedSources}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)
