package app

var scenarioEditorPageTemplate = newPageTemplate("scenario-editor-page", `<div class="page-heading">
			<div>
				<h1>Scenario Editor</h1>
			</div>
			<div class="page-actions">
				<a class="button-link secondary" href="/scenarios">Scenarios</a>
			</div>
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section class="scenario-editor-layout">
				<form method="post" action="/scenarios/editor/validate" class="panel scenario-editor-form">
					<div class="section-heading">
						<h2>Draft</h2>
						<span>YAML</span>
					</div>
					<label class="form-row">Scenario YAML
						<textarea name="scenario_document" class="scenario-editor-textarea" spellcheck="false" required>{{.Draft}}</textarea>
					</label>
					<div class="inline-actions">
						<button type="submit">Validate Draft</button>
						<a class="button-link secondary" href="/scenarios">Cancel</a>
					</div>
				</form>

				<section class="panel scenario-editor-preview">
					<div class="section-heading">
						<h2>Validation Preview</h2>
						{{if .Preview.HasResult}}<span class="status {{.Preview.StatusClass}}">{{.Preview.Status}}</span>{{end}}
					</div>
					{{if .Preview.HasResult}}
						{{if .Preview.Valid}}
							<div class="detail-list">
								<span>Name</span>
								<strong>{{.Preview.Name}}</strong>
							</div>
							<div class="scenario-editor-summary">
								<div class="detail-list">
									<span>Clock Start</span>
									<strong>{{.Preview.ClockStart}}</strong>
								</div>
								<div class="detail-list">
									<span>Template</span>
									<strong>{{.Preview.OrganizationTemplate}}</strong>
								</div>
								<div class="detail-list">
									<span>Events</span>
									<strong>{{.Preview.EventCount}}</strong>
								</div>
								<div class="detail-list">
									<span>Checks</span>
									<strong>{{.Preview.CheckCount}}</strong>
								</div>
								<div class="detail-list">
									<span>Simulated Window</span>
									<strong>{{.Preview.SimulatedDuration}}</strong>
								</div>
								{{if .Preview.RandomSeed}}
									<div class="detail-list">
										<span>Random Seed</span>
										<strong>{{.Preview.RandomSeed}}</strong>
									</div>
								{{end}}
							</div>
							<div class="table-wrap scenario-editor-events">
								<table class="dense-table">
									<thead>
										<tr>
											<th>#</th>
											<th>Event</th>
											<th>Action</th>
											<th>Schedule</th>
											<th>Target</th>
										</tr>
									</thead>
									<tbody>
										{{range .Preview.Events}}
											<tr>
												<td>{{.Sequence}}</td>
												<td><strong>{{.ID}}</strong></td>
												<td>{{.Action}}</td>
												<td>{{.Schedule}}</td>
												<td>{{.Target}}</td>
											</tr>
										{{else}}
											<tr><td colspan="5">No events</td></tr>
										{{end}}
									</tbody>
								</table>
							</div>
						{{else}}
							<ul class="validation-list">
								{{range .Preview.Problems}}
									<li>{{.}}</li>
								{{end}}
							</ul>
						{{end}}
					{{else}}
						<section class="empty compact-empty">
							<h2>No Preview</h2>
						</section>
					{{end}}
				</section>
			</section>
		{{end}}
`)

var scenariosPageTemplate = newPageTemplate("scenarios-page", `<div class="page-heading">
			<div>
				<h1>Scenarios</h1>
			</div>
			{{if .WorkspaceReady}}
				<div class="page-actions">
					<a class="button-link secondary" href="/scenarios/editor">Scenario Editor</a>
				</div>
			{{end}}
		</div>

		{{template "ui.notices" .Notices}}

		{{if not .WorkspaceReady}}
			{{template "ui.empty-state" .WorkspaceEmptyState}}
		{{else}}
			<section>
				<div class="section-heading">
					<h2>Available Scenarios</h2>
					<span>{{len .Scenarios}} labs</span>
				</div>
				<div class="scenario-grid">
					{{range .Scenarios}}
						<article class="scenario-card">
							<div class="scenario-card-header">
								<div>
									<h3>{{.Name}}</h3>
									<small>{{.Key}}</small>
								</div>
								<span class="status">{{.Phase}}</span>
							</div>
							<div class="detail-list">
								<span>Objective</span>
								<strong>{{.Objective}}</strong>
							</div>
							<div class="scenario-meta-grid">
								<div class="detail-list">
									<span>Estimated Duration</span>
									<strong>{{.EstimatedDuration}}</strong>
								</div>
								<div class="detail-list">
									<span>Simulated Window</span>
									<strong>{{.SimulatedDuration}}</strong>
								</div>
								<div class="detail-list">
									<span>Events</span>
									<strong>{{.EventCount}}</strong>
									{{if .CheckCount}}<small>{{.CheckCount}} checks</small>{{end}}
								</div>
							</div>
							{{if .HasLastRun}}
								<div class="detail-list scenario-run-detail">
									<span>Last Run</span>
									<strong><span class="status {{.LastRun.StatusClass}}">{{.LastRun.Status}}</span> {{.LastRun.Events}} events</strong>
									<small>{{.LastRun.ID}}</small>
									{{if .LastRun.ProgressState}}<small><span class="status {{.LastRun.ProgressClass}}">{{.LastRun.ProgressState}}</span> {{.LastRun.ProgressSummary}}{{if .LastRun.CheckSummary}}, {{.LastRun.CheckSummary}}{{end}}</small>{{end}}
									{{if .LastRun.ErrorMessage}}<small>{{.LastRun.ErrorMessage}}</small>{{end}}
								</div>
							{{end}}
							<div class="scenario-actions">
								<form method="post" action="/scenarios/launch">
									<input type="hidden" name="scenario_key" value="{{.Key}}">
									<button type="submit">{{.StartLabel}}</button>
								</form>
								{{if .HasLastRun}}<a class="button-link secondary" href="{{.ResumePath}}">{{.ResumeLabel}}</a>{{end}}
								{{if .FeedbackPath}}<a class="button-link secondary" href="{{.FeedbackPath}}">Feedback Report</a>{{end}}
							</div>
							{{if and .HasLastRun $.WorkspaceActionReady}}
								<div class="scenario-management-actions">
									<form method="post" action="/scenarios/reset">
										<input type="hidden" name="scenario_key" value="{{.Key}}">
										<button type="submit">Reset to Seed</button>
									</form>
									<form method="post" action="/scenarios/clone" class="scenario-clone-form">
										<label class="form-row">Clone Workspace Path
											<input name="clone_workspace_path" value="{{.ClonePath}}" required>
										</label>
										<button type="submit">Clone Workspace</button>
									</form>
									<form method="post" action="/scenarios/archive">
										<input type="hidden" name="scenario_run_id" value="{{.LastRun.ID}}">
										<button type="submit">Archive Review Bundle</button>
									</form>
								</div>
							{{end}}
						</article>
					{{else}}
						<section class="empty">
							<h2>No Scenarios</h2>
							<p>No packaged scenarios are available.</p>
						</section>
					{{end}}
				</div>
			</section>

			<section>
				<div class="section-heading">
					<h2>Recent Runs</h2>
					<span>{{len .RecentRuns}} attempts</span>
				</div>
				<div class="table-wrap">
					<table class="dense-table">
						{{template "ui.dense-table-head" .Tables.RecentRuns}}
						<tbody>
							{{range .RecentRuns}}
								<tr>
									<td><strong>{{.DefinitionName}}</strong><small>{{.ID}}</small></td>
									<td><span class="status {{.StatusClass}}">{{.Status}}</span></td>
									<td>{{if .ProgressState}}<span class="status {{.ProgressClass}}">{{.ProgressState}}</span><small>{{.ProgressSummary}}{{if .CheckSummary}}, {{.CheckSummary}}{{end}}</small>{{else}}-{{end}}</td>
									<td>{{.Events}}</td>
									<td>{{.ResourcesCreated}}</td>
									<td>{{.UsageEvents}}</td>
									<td>{{.BillsIssued}}</td>
									<td>{{.CurrentEventID}}</td>
									<td>{{if .FeedbackPath}}<a href="{{.FeedbackPath}}">Report</a>{{else}}-{{end}}</td>
									<td>{{if .CompletedAt}}{{.CompletedAt}}{{else}}{{.StartedAt}}{{end}}{{if .ErrorMessage}}<small>{{.ErrorMessage}}</small>{{end}}</td>
								</tr>
							{{else}}
								{{template "ui.dense-table-empty-row" $.Tables.RecentRuns}}
							{{end}}
						</tbody>
					</table>
				</div>
			</section>
		{{end}}
`)
