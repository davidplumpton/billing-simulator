package app

var organizationPageTemplate = newPageTemplate("organization-page", `<div class="page-heading">
			<div>
				<h1>Organization</h1>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="organization-hero">
				<div>
					<h2>{{.Organization.Name}}</h2>
					<strong>{{.Organization.ManagementAccountID}}</strong>
					<small>Management account</small>
				</div>
				<div class="organization-meta">
					<div>
						<span>Template</span>
						<strong>{{.Organization.TemplateKey}}</strong>
					</div>
					<div>
						<span>Organization ID</span>
						<strong>{{.Organization.OrganizationID}}</strong>
					</div>
					<div>
						<span>Created</span>
						<strong>{{.Organization.CreatedAt}}</strong>
					</div>
				</div>
			</section>

			<section class="organization-summary-grid">
				<div class="state-card">
					<span>Roots</span>
					<strong>{{.Summary.RootCount}}</strong>
				</div>
				<div class="state-card">
					<span>OUs</span>
					<strong>{{.Summary.UnitCount}}</strong>
				</div>
				<div class="state-card">
					<span>Accounts</span>
					<strong>{{.Summary.AccountCount}}</strong>
				</div>
				<div class="state-card">
					<span>Suspended</span>
					<strong>{{.Summary.SuspendedCount}}</strong>
				</div>
			</section>

			<section class="clock-strip">
				<div>
					<h2>Simulator Clock</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
			</section>

			<section class="form-grid">
				<form method="post" action="/organization/accounts/create" class="panel">
					<h2>Create Account</h2>
					<input type="hidden" name="organization_id" value="{{.Organization.OrganizationID}}">
					<div class="fields">
						<label>Account ID
							<input name="account_id" value="{{.SuggestedAccountID}}" inputmode="numeric" required>
						</label>
						<label>OU
							<select name="parent_unit_id" required>
								{{range .UnitOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Name
							<input name="account_name" value="Workload Expansion" required>
						</label>
						<label>Email
							<input name="account_email" value="workload-expansion@anycompany.example" required>
						</label>
						<label class="wide">Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Create Account</button>
				</form>

				<form method="post" action="/organization/accounts/move" class="panel compact">
					<h2>Move Account</h2>
					<div class="fields">
						<label>Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Target OU
							<select name="parent_unit_id" required>
								{{range .UnitOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Move Account</button>
				</form>

				<form method="post" action="/organization/accounts/suspend" class="panel compact">
					<h2>Suspend Account</h2>
					<div class="fields">
						<label>Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Suspend Account</button>
				</form>

				<form method="post" action="/organization/accounts/close" class="panel compact">
					<h2>Close Account</h2>
					<div class="fields">
						<label>Account
							<select name="account_id" required>
								{{range .AccountOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label>Effective
							<input type="datetime-local" name="effective_at" value="{{.DefaultEffectiveAt}}" required>
						</label>
					</div>
					<button type="submit">Close Account</button>
				</form>
			</section>

			<section class="organization-layout">
				<div class="panel organization-tree-panel">
					<h2>Hierarchy</h2>
					<div class="organization-tree" role="tree">
						{{range .Tree}}
							<div class="org-tree-row {{.DepthClass}} {{.KindClass}}" role="treeitem">
								<span class="org-tree-kind">{{.KindLabel}}</span>
								<div>
									<strong>{{.Name}}</strong>
									<small>{{.Detail}}{{if .ID}} - {{.ID}}{{end}}</small>
								</div>
								{{if .Status}}<span class="status {{.StatusClass}}">{{.Status}}</span>{{end}}
								{{if .HasBillingLink}}<a href="{{.BillsPath}}">Bills</a>{{end}}
							</div>
						{{end}}
					</div>
				</div>

				<div>
					<div class="section-heading">
						<h2>Account Detail</h2>
						<span>{{len .Accounts}} accounts</span>
					</div>
					<div class="account-panel-grid">
						{{range .Accounts}}
							<article class="account-panel">
								<div class="account-panel-header">
									<div>
										<h3>{{.Name}}</h3>
										<small>{{.AccountID}}</small>
									</div>
									<span class="status {{.StatusClass}}">{{.Status}}</span>
								</div>
								<div class="detail-list">
									<span>OU Path</span>
									<strong>{{.OUPath}}</strong>
								</div>
								<div class="detail-list">
									<span>Owner</span>
									<strong>{{.Owner}}</strong>
								</div>
								<div class="detail-list">
									<span>Cost Center</span>
									<strong>{{.CostCenter}}</strong>
								</div>
								<div class="detail-list">
									<span>Product</span>
									<strong>{{.Product}}</strong>
								</div>
								<div class="detail-list">
									<span>Environment</span>
									<strong>{{.Environment}}</strong>
								</div>
								<div class="detail-list">
									<span>Lifecycle</span>
									<strong>{{.Lifecycle}}</strong>
								</div>
								<div class="detail-list">
									<span>Billing Role</span>
									<strong>{{.BillingVisibilityRole}}</strong>
								</div>
								<div class="detail-list">
									<span>Payer</span>
									<strong>{{.PayerAccountID}}</strong>
								</div>
								<div class="detail-list">
									<span>Email</span>
									<strong>{{.Email}}</strong>
								</div>
								<div class="account-actions">
									<a class="button-link" href="{{.ResourcePath}}">Resources</a>
									<a class="button-link" href="{{.BillsPath}}">Bills</a>
								</div>
							</article>
						{{end}}
					</div>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Account Directory</h2>
					<span>{{.Summary.ActiveCount}} active, {{.Summary.SuspendedCount}} suspended, {{.Summary.ClosedCount}} closed</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Accounts}}
						<tbody>
							{{range .Accounts}}
								<tr>
									<td><strong>{{.Name}}</strong><small>{{.AccountID}} - {{.AccountType}}</small></td>
									<td>{{.OUPath}}</td>
									<td>{{.Owner}}<small>{{.CostCenter}}</small></td>
									<td><strong>{{.Product}}</strong><small>{{.Environment}} - {{.Lifecycle}}</small></td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span></td>
									<td>{{.PayerAccountID}}</td>
									<td>{{.BillingVisibilityRole}}</td>
									<td><a href="{{.ResourcePath}}">Resources</a> - <a href="{{.BillsPath}}">Bills</a></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Accounts}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Lifecycle History</h2>
					<span>{{len .LifecycleEvents}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LifecycleEvents}}
						<tbody>
							{{range .LifecycleEvents}}
								<tr>
									<td><strong>{{.Account}}</strong></td>
									<td>{{.Event}}</td>
									<td>{{.ParentMovement}}</td>
									<td>{{.StatusChange}}</td>
									<td>{{.EffectiveAt}}</td>
									<td>{{.Source}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.LifecycleEvents}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
