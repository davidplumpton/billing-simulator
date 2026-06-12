package app

var paymentsPageTemplate = newPageTemplate("payments-page", `<div class="page-heading">
			<div>
				<h1>Payments</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="{{.BillsPath}}">Bills</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section>
				<form method="get" action="/payments" class="filter-form">
					{{template "ui.select-field" .Filters.ViewerRoleField}}
					{{template "ui.input-field" .Filters.ViewerAccountField}}
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
			</section>

			<section class="state-grid" aria-label="Payment state totals">
				{{range .StateCards}}
					<div class="state-card">
						<span>{{.Label}}</span>
						<strong>{{.Total}}</strong>
						<small>{{.Count}} invoices</small>
					</div>
				{{end}}
			</section>

			<section>
				<div class="section-heading">
					<h2>Due Invoices</h2>
					<span>{{len .Invoices}} invoices</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table payments-table">
						{{template "ui.dense-table-head" .Tables.Invoices}}
						<tbody>
							{{range .Invoices}}
								<tr>
									<td><a href="{{.InvoicePath}}"><strong>{{.InvoiceID}}</strong></a><small>{{.InvoiceDate}}</small></td>
									<td>{{.BillingPeriod}}</td>
									<td>{{.PayerAccountID}}</td>
									<td><strong>{{.BillID}}</strong><small>{{.BillState}}</small></td>
									<td><span class="status">{{.PaymentStatus}}</span></td>
									<td>{{.AmountDue}}</td>
									<td>{{.AmountPaid}}</td>
									<td>{{.DueDate}}</td>
									<td>{{.LastFailureReason}}</td>
									<td class="actions-cell">
										<div class="inline-actions">
											{{if .CanSchedule}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="schedule" type="submit">Schedule</button>
												</form>
											{{end}}
											{{if .CanProcess}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="process" type="submit">Retry</button>
												</form>
											{{end}}
											{{if .CanMarkDue}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="mark_due" type="submit">Mark Due</button>
												</form>
											{{end}}
											{{if .CanMarkPastDue}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>As Of
														<input type="date" name="occurred_at" value="{{.PastDueDate}}">
													</label>
													<button name="action" value="mark_past_due" type="submit">Past Due</button>
												</form>
											{{end}}
											{{if .CanFail}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>Reason
														<input name="reason" value="Payment attempt failed">
													</label>
													<button name="action" value="fail" type="submit">Fail</button>
												</form>
											{{end}}
											{{if .CanCollect}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>Amount
														<input name="amount" inputmode="decimal" value="{{.AmountDueValue}}">
													</label>
													<button name="action" value="collect" type="submit">Collect</button>
												</form>
											{{end}}
											{{if .CanRefund}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="invoice_obligation_id" value="{{.InvoiceObligationID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<label>Amount
														<input name="amount" inputmode="decimal" value="{{.AmountPaidValue}}">
													</label>
													<button name="action" value="refund" type="submit">Refund</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.Invoices}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Payment History</h2>
					<span>{{len .History}} events</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.PaymentHistory}}
						<tbody>
							{{range .History}}
								<tr>
									<td>{{.InvoiceID}}</td>
									<td>{{.Transition}}</td>
									<td>{{.FromStatus}}</td>
									<td><span class="status">{{.ToStatus}}</span></td>
									<td>{{.Amount}} <small>{{.CurrencyCode}}</small></td>
									<td>{{.Reason}}</td>
									<td>{{.OccurredAt}}</td>
									<td>{{.CreatedAt}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PaymentHistory}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Payment Setup</h2>
					<span>{{len .PaymentMethods}} methods</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table payments-table">
						{{template "ui.dense-table-head" .Tables.PaymentMethods}}
						<tbody>
							{{range .PaymentMethods}}
								<tr>
									<td>{{.PayerAccountID}}</td>
									<td><strong>{{.ProfileName}}</strong><small>{{.ProfileID}}</small></td>
									<td>{{.BillTo}}</td>
									<td><strong>{{.MethodDisplayName}}</strong><small>{{.MethodType}}</small></td>
									<td><span class="status">{{.Status}}</span></td>
									<td>{{.DefaultLabel}}</td>
									<td>{{.UnappliedFunds}}</td>
									<td>{{.FailureReason}}</td>
									<td class="actions-cell">
										<div class="inline-actions">
											{{if .CanFix}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="method_id" value="{{.MethodID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="fix_method" type="submit">Fix Method</button>
												</form>
											{{end}}
											{{if .CanSetDefault}}
												<form method="post" action="/payments/action">
													<input type="hidden" name="method_id" value="{{.MethodID}}">
													<input type="hidden" name="viewer_role" value="{{.ViewerRole}}">
													<input type="hidden" name="viewer_account_id" value="{{.ViewerAccountID}}">
													<button name="action" value="set_default_method" type="submit">Set Default</button>
												</form>
											{{end}}
										</div>
									</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.PaymentMethods}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
