package app

var proFormaPageTemplate = newPageTemplate("pro-forma-page", `<div class="page-heading">
			<div>
				<h1>Pro Forma</h1>
			</div>
		</div>

		<div id="pro-forma-refresh" data-partial-surface="pro-forma">
			{{template "pro-forma.refresh" .}}
		</div>

{{define "pro-forma.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Internal Showback</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.BillingPeriodStart}} to {{.BillingPeriodEnd}}</small>
				</div>
				<div class="page-actions">
					<a class="button-link secondary" href="/cost-categories">Cost Categories</a>
					<a class="button-link secondary" href="/bills">Bills</a>
					<a class="button-link" href="/pro-forma">Refresh</a>
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
				<form method="post" action="/pro-forma/pricing-plans/create" class="panel compact">
					<h2>New Pricing Plan</h2>
					<label class="form-row">Name
						<input name="name" required>
					</label>
					<label class="form-row">Description
						<input name="description">
					</label>
					<button type="submit">Create Plan</button>
				</form>

				{{if .PricingPlanOptions}}
					<form method="post" action="/pro-forma/pricing-rules/create" class="panel compact">
						<h2>Service Rate</h2>
						<label class="form-row">Plan
							<select name="pricing_plan_id" required>
								{{range .PricingPlanOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Service Code
							<input name="service_code" value="AmazonEC2" required>
						</label>
						<label class="form-row">Multiplier %
							<input name="multiplier_percent" value="100" inputmode="decimal" required>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Save Rate</button>
					</form>

					<form method="post" action="/pro-forma/billing-groups/create" class="panel compact">
						<h2>New Billing Group</h2>
						<label class="form-row">Name
							<input name="name" required>
						</label>
						<label class="form-row">Payer Account
							<input name="payer_account_id" value="999988887777" required>
						</label>
						<label class="form-row">Plan
							<select name="pricing_plan_id" required>
								{{range .PricingPlanOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Create Group</button>
					</form>
				{{end}}

				{{if and .BillingGroupOptions .AccountOptions}}
					<form method="post" action="/pro-forma/accounts/assign" class="panel compact">
						<h2>Assign Account</h2>
						<label class="form-row">Group
							<select name="billing_group_id" required>
								{{range .BillingGroupOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<button type="submit">Assign Account</button>
					</form>

					<form method="post" action="/pro-forma/refresh" class="panel compact">
						<h2>Refresh Rows</h2>
						<label class="form-row">Group
							<select name="billing_group_id">
								<option value="">All groups</option>
								{{range .BillingGroupOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Period Start
							<input name="billing_period_start" value="{{.BillingPeriodStart}}" required>
						</label>
						<label class="form-row">Period End
							<input name="billing_period_end" value="{{.BillingPeriodEnd}}" required>
						</label>
						<label class="form-row">Payer Account
							<input name="payer_account_id" value="999988887777">
						</label>
						<button type="submit">Refresh Rows</button>
					</form>

					<form method="post" action="/pro-forma/custom-line-items/create" class="panel compact">
						<h2>Custom Item</h2>
						<label class="form-row">Group
							<select name="billing_group_id" required>
								{{range .BillingGroupOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Type
							<select name="line_item_type" required>
								{{range .CustomTypeOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Name
							<input name="name" required>
						</label>
						<label class="form-row">Amount USD
							<input name="amount_usd" value="0.00" inputmode="decimal">
						</label>
						<label class="form-row">Period Start
							<input name="billing_period_start" value="{{.BillingPeriodStart}}" required>
						</label>
						<label class="form-row">Period End
							<input name="billing_period_end" value="{{.BillingPeriodEnd}}" required>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Add Item</button>
					</form>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Pricing Plans</h2>
					<span>{{len .PricingPlans}} plans</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PricingPlans}}
						<tbody>
							{{range .PricingPlans}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.CurrencyCode}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.RuleCount}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PricingPlans}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Pricing Rules</h2>
					<span>{{len .PricingRules}} rules</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PricingRules}}
						<tbody>
							{{range .PricingRules}}
								<tr>
									<td>{{.PricingPlanName}}</td>
									<td><strong>{{.ServiceCode}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{.Multiplier}}</td>
									<td><span class="status">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PricingRules}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Billing Groups</h2>
					<span>{{len .BillingGroups}} groups</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.BillingGroups}}
						<tbody>
							{{range .BillingGroups}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.PricingPlanName}}</td>
									<td>{{.AccountCount}}</td>
									<td><span class="status">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.BillingGroups}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Account Assignments</h2>
					<span>{{len .AccountAssignments}} accounts</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.AccountAssignments}}
						<tbody>
							{{range .AccountAssignments}}
								<tr>
									<td>{{.BillingGroupName}}</td>
									<td><strong>{{.AccountLabel}}</strong><small>{{.AccountID}}</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.AccountAssignments}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Showback Summary</h2>
					<span>{{len .Summaries}} groups</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Summaries}}
						<tbody>
							{{range .Summaries}}
								<tr>
									<td><strong>{{.BillingGroupName}}</strong><small>{{.PricingPlanName}}</small><small>{{.PayerAccountID}} / {{.CurrencyCode}}</small></td>
									<td>{{.Period}}</td>
									<td>{{.SourceCost}}</td>
									<td>{{if .CustomLineItems}}<strong>{{.CustomAmount}}</strong>{{else}}{{.CustomAmount}}{{end}}</td>
									<td><strong>{{.ProFormaCost}}</strong></td>
									<td>{{if .AdjustmentMicros}}<strong>{{.Adjustment}}</strong>{{else}}{{.Adjustment}}{{end}}</td>
									<td>{{.SourceActivityText}} / {{.CustomActivityText}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Summaries}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Custom Items</h2>
					<span>{{len .CustomLineItems}} items</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.CustomLineItems}}
						<tbody>
							{{range .CustomLineItems}}
								<tr>
									<td>{{.BillingGroupName}}</td>
									<td><span class="status">{{.Type}}</span></td>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{if .AmountMicros}}<strong>{{.Amount}}</strong>{{else}}{{.Amount}}{{end}}</td>
									<td>{{.Period}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.CustomLineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Generated Rows</h2>
					<span>{{len .LineItems}} rows</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.BillingGroupName}}</strong><small>{{.SourceID}}</small></td>
									<td>{{.Service}}<small>{{.UsageType}}</small><small>{{.Status}}</small></td>
									<td>{{.AccountID}}</td>
									<td>{{.SourceCost}}</td>
									<td><strong>{{.ProFormaCost}}</strong></td>
									<td>{{.Adjustment}}</td>
									<td>{{.Multiplier}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LineItems}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
{{end}}
`)
