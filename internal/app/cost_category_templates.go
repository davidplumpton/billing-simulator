package app

var costCategoriesPageTemplate = newPageTemplate("cost-categories-page", `<div class="page-heading">
			<div>
				<h1>Cost Categories</h1>
			</div>
		</div>

		<div id="cost-categories-refresh" data-partial-surface="cost-categories">
			{{template "cost-categories.refresh" .}}
		</div>

{{define "cost-categories.refresh"}}
		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="clock-strip">
				<div>
					<h2>Cost Category Preview</h2>
					<strong>{{.ClockCurrentTime}}</strong>
					<small>{{.ClockBillingPeriod}}</small>
				</div>
				<div class="page-actions">
					<a class="button-link secondary" href="/resources">Resources</a>
					<a class="button-link secondary" href="/tags">Tags</a>
					<a class="button-link" href="/cost-categories{{if .SelectedCategoryID}}?category_id={{.SelectedCategoryID}}{{end}}">Refresh Preview</a>
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
				<form method="post" action="/cost-categories/categories/create" class="panel compact">
					<h2>New Category</h2>
					<label class="form-row">Name
						<input name="name" required>
					</label>
					<label class="form-row">Default Value
						<input name="default_value" value="Uncategorized" required>
					</label>
					<label class="form-row">Description
						<input name="description">
					</label>
					<button type="submit">Create Category</button>
				</form>

				{{if .CategoryOptions}}
					<form method="post" action="/cost-categories/rules/create" class="panel compact">
						<h2>New Rule</h2>
						<label class="form-row">Category
							<select name="category_id" required>
								{{range .CategoryOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Order
							<input name="rule_order" value="{{.NextRuleOrder}}" inputmode="numeric" required>
						</label>
						<label class="form-row">Value
							<input name="value" required>
						</label>
						<label class="form-row">Dimension
							<select name="dimension" required>
								{{range .DimensionOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Operator
							<select name="operator" required>
								{{range .OperatorOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Values
							<input name="values" required>
						</label>
						<label class="form-row">Tag Key
							<input name="tag_key">
						</label>
						<label class="form-row">Referenced Category
							<select name="referenced_category_id">
								<option value=""></option>
								{{range .CategoryOptions}}<option value="{{.Value}}">{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Create Rule</button>
					</form>

					<form method="post" action="/cost-categories/splits/create" class="panel compact">
						<h2>New Split Rule</h2>
						<label class="form-row">Category
							<select name="category_id" required>
								{{range .CategoryOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Source Value
							<input name="source_value" required>
						</label>
						<label class="form-row">Method
							<select name="method" required>
								{{range .SplitMethodOptions}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Label}}</option>{{end}}
							</select>
						</label>
						<label class="form-row">Target Values
							<textarea name="target_values" rows="3" required></textarea>
						</label>
						<label class="form-row">Fixed Shares
							<textarea name="fixed_share_micros" rows="3"></textarea>
						</label>
						<label class="form-row">Description
							<input name="description">
						</label>
						<button type="submit">Create Split Rule</button>
					</form>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Categories</h2>
					<span>{{len .Categories}} categories</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.Categories}}
						<tbody>
							{{range .Categories}}
								<tr>
									<td><strong>{{.Name}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{.DefaultValue}}</td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.RuleCount}}</td>
									<td>
										{{if .Selected}}<span class="status">Selected</span>{{else}}<a class="button-link secondary" href="/cost-categories?category_id={{.ID}}">Preview</a>{{end}}
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Categories}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Rule Order Effects</h2>
					<span>{{.SelectedCategory}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.RuleEffects}}
						<tbody>
							{{range .RuleEffects}}
								<tr>
									<td>{{.Order}}</td>
									<td><strong>{{.Value}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}</td>
									<td>{{range .Conditions}}<small>{{.}}</small>{{end}}</td>
									<td>{{.MatchedSpend}}<small>{{.MatchedItems}} line items</small></td>
									<td>{{.ShadowedSpend}}<small>{{.ShadowedItems}} line items</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.RuleEffects}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Split Charge Rules</h2>
					<span>{{.SelectedCategory}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.SplitRules}}
						<tbody>
							{{range .SplitRules}}
								<tr>
									<td><strong>{{.SourceValue}}</strong>{{if .Description}}<small>{{.Description}}</small>{{end}}<small>{{.ID}}</small></td>
									<td>{{.Method}}</td>
									<td>{{.TargetSummary}}</td>
									<td><span class="status">{{.Status}}</span></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.SplitRules}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Allocation Comparison</h2>
					<span>{{len .AllocationRows}} values</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.AllocationComparison}}
						<tbody>
							{{range .AllocationRows}}
								<tr>
									<td><strong>{{.Value}}</strong><small>{{.PayerAccountID}} / {{.CurrencyCode}}</small></td>
									<td>{{.RawCost}}</td>
									<td>{{.CategoryCost}}</td>
									<td>{{.SplitAmount}}</td>
									<td><strong>{{.TotalAllocatedCost}}</strong></td>
									<td>{{if .UnallocatedResidualCostMicros}}<strong>{{.UnallocatedResidual}}</strong>{{else}}{{.UnallocatedResidual}}{{end}}</td>
									<td><small>{{.RawActivity}}</small><small>{{.SourceActivity}}</small><small>{{.AllocationActivity}}</small></td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.AllocationComparison}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Line Item Preview</h2>
					<span>{{len .LineItems}} rows{{if .HasMoreLineItems}} shown{{end}}</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.LineItems}}
						<tbody>
							{{range .LineItems}}
								<tr>
									<td><strong>{{.ResourceID}}</strong><small>{{.ID}}</small><small>{{.AccountID}} / {{.RegionCode}}</small></td>
									<td>{{.Service}}<small>{{.UsageType}}</small><small>{{.Status}}</small></td>
									<td>{{.Cost}}</td>
									<td>{{.BeforeValue}}</td>
									<td><strong>{{.PreviewValue}}</strong></td>
									<td>
										{{.MatchedRule}}
										{{if .ShadowedRules}}<div class="tags">{{range .ShadowedRules}}<span>{{.}}</span>{{end}}</div>{{end}}
									</td>
									<td>{{if .Tags}}<div class="tags">{{range .Tags}}<span>{{.}}</span>{{end}}</div>{{end}}</td>
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
