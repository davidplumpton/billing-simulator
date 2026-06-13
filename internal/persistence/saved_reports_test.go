package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"aws-billing-simulator/internal/billingvisibility"
)

func TestSavedReportRepositoryCreatesUpdatesRunsListsAndDeletesReports(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSavedReportRepository(db)

	report, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-monthly-service",
		Name:           "Monthly cost by service",
		Description:    "Track AnyCompany monthly service cost",
		OwnerAccountID: "999988887777",
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Filters: map[string][]string{
			"service":        {"AmazonEC2", "AmazonS3"},
			"tag:app":        {"storefront"},
			"linked_account": {"111122223333"},
		},
		Groupings: []SavedReportGrouping{
			{Type: "dimension", Key: "service"},
		},
		Metrics:   []string{"unblended_cost", "usage_quantity"},
		ChartType: "bar",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if report.ID != "saved-report-monthly-service" ||
		report.LastRunStatus != savedReportStatusNeverRun ||
		report.LastRunAt != "" ||
		report.LastRunMetric != defaultSavedReportMetric ||
		report.LastRunMetricTotalMicros != 0 ||
		report.Filters["service"][1] != "AmazonS3" ||
		len(report.Groupings) != 1 ||
		report.Groupings[0].Key != "service" ||
		report.Metrics[1] != "usage_quantity" {
		t.Fatalf("created report = %+v, want stored definition with never-run metadata", report)
	}

	byName, err := repo.GetByName(ctx, "999988887777", "management-account", "monthly COST by service")
	if err != nil {
		t.Fatalf("GetByName() error = %v", err)
	}
	if byName.ID != report.ID {
		t.Fatalf("GetByName() = %+v, want report %s", byName, report.ID)
	}

	updated, err := repo.Update(ctx, SavedReportUpdateRequest{
		ID:             report.ID,
		Name:           "Daily storefront cost by service",
		Description:    "Narrowed to daily Storefront visibility",
		OwnerAccountID: "999988887777",
		OwnerRole:      "finance",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-02-08",
		Granularity:    "daily",
		Filters: map[string][]string{
			"tag:app": {"storefront"},
		},
		Groupings: []SavedReportGrouping{
			{Type: "dimension", Key: "service"},
			{Type: "tag", Key: "env"},
		},
		Metrics:   []string{"unblended_cost"},
		ChartType: "line",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != "Daily storefront cost by service" ||
		updated.OwnerRole != "finance" ||
		updated.Granularity != "daily" ||
		updated.ChartType != "line" ||
		len(updated.Groupings) != 2 ||
		updated.Groupings[1].Type != "tag" ||
		updated.LastRunStatus != savedReportStatusNeverRun {
		t.Fatalf("updated report = %+v, want replaced definition preserving run metadata", updated)
	}

	ran, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:                       report.ID,
		RunAt:                    "2026-02-08T00:00:00Z",
		Status:                   savedReportStatusSucceeded,
		RowCount:                 7,
		TotalUnblendedCostMicros: 123_456_789,
		Metric:                   "usage_quantity",
		MetricTotalMicros:        7_000_000,
	})
	if err != nil {
		t.Fatalf("RecordLastRun() error = %v", err)
	}
	if ran.LastRunAt != "2026-02-08T00:00:00Z" ||
		ran.LastRunStatus != savedReportStatusSucceeded ||
		ran.LastRunRowCount != 7 ||
		ran.LastRunTotalUnblendedCostMicros != 123_456_789 ||
		ran.LastRunMetric != "usage_quantity" ||
		ran.LastRunMetricTotalMicros != 7_000_000 ||
		ran.LastRunError != "" {
		t.Fatalf("last-run metadata = %+v, want successful run summary", ran)
	}

	second, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-accounts",
		Name:           "Monthly cost by linked account",
		OwnerAccountID: "111122223333",
		OwnerRole:      "member-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings: []SavedReportGrouping{
			{Type: "dimension", Key: "linked_account"},
		},
	})
	if err != nil {
		t.Fatalf("Create(second) error = %v", err)
	}
	if second.Granularity != defaultSavedReportGranularity ||
		second.ChartType != defaultSavedReportChartType ||
		second.Metrics[0] != defaultSavedReportMetric {
		t.Fatalf("second report defaults = %+v, want default granularity/chart/metric", second)
	}

	ownerReports, err := repo.List(ctx, SavedReportListRequest{OwnerAccountID: "999988887777"})
	if err != nil {
		t.Fatalf("List(owner) error = %v", err)
	}
	if len(ownerReports) != 1 || ownerReports[0].ID != report.ID {
		t.Fatalf("owner reports = %+v, want only management payer report", ownerReports)
	}
	allReports, err := repo.List(ctx, SavedReportListRequest{Limit: 10})
	if err != nil {
		t.Fatalf("List(all) error = %v", err)
	}
	if len(allReports) != 2 {
		t.Fatalf("all reports count = %d, want 2", len(allReports))
	}

	if err := repo.Delete(ctx, report.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.Get(ctx, report.ID); !errors.Is(err, ErrSavedReportNotFound) || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("Get(deleted) error = %v, want ErrSavedReportNotFound with compatible message", err)
	}
}

func TestSavedReportRepositoryRecordsSignedSelectedMetricRunTotal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSavedReportRepository(db)

	report, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-net-cost-run",
		Name:           "Net cost run",
		OwnerAccountID: "999988887777",
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Metrics:        []string{"net_cost"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	ran, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:                       report.ID,
		RunAt:                    "2026-02-08T00:00:00Z",
		Status:                   savedReportStatusSucceeded,
		RowCount:                 3,
		TotalUnblendedCostMicros: 200_000,
		Metric:                   "net_cost",
		MetricTotalMicros:        -50_000,
	})
	if err != nil {
		t.Fatalf("RecordLastRun() error = %v", err)
	}
	if ran.LastRunMetric != "net_cost" ||
		ran.LastRunMetricTotalMicros != -50_000 ||
		ran.LastRunTotalUnblendedCostMicros != 200_000 {
		t.Fatalf("last-run metric metadata = %+v, want signed selected metric plus unblended total", ran)
	}
}

func TestSavedReportRepositoryReturnsSentinelNotFoundErrors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSavedReportRepository(db)

	if _, err := repo.Get(ctx, "missing-report"); !errors.Is(err, ErrSavedReportNotFound) {
		t.Fatalf("Get(missing) error = %v, want ErrSavedReportNotFound", err)
	}
	if _, err := repo.GetForOwner(ctx, "missing-report", "999988887777", "finance"); !errors.Is(err, ErrSavedReportNotFound) {
		t.Fatalf("GetForOwner(missing) error = %v, want ErrSavedReportNotFound", err)
	}
	if _, err := repo.GetByName(ctx, "999988887777", "finance", "Missing report"); !errors.Is(err, ErrSavedReportNotFound) {
		t.Fatalf("GetByName(missing) error = %v, want ErrSavedReportNotFound", err)
	}
	if _, err := repo.Update(ctx, SavedReportUpdateRequest{
		ID:             "missing-report",
		Name:           "Missing report",
		OwnerAccountID: "999988887777",
		OwnerRole:      "finance",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	}); !errors.Is(err, ErrSavedReportNotFound) {
		t.Fatalf("Update(missing) error = %v, want ErrSavedReportNotFound", err)
	}
	if _, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:     "missing-report",
		RunAt:  "2026-02-01T00:00:00Z",
		Status: savedReportStatusSucceeded,
	}); !errors.Is(err, ErrSavedReportNotFound) {
		t.Fatalf("RecordLastRun(missing) error = %v, want ErrSavedReportNotFound", err)
	}
	if err := repo.Delete(ctx, "missing-report"); !errors.Is(err, ErrSavedReportNotFound) {
		t.Fatalf("Delete(missing) error = %v, want ErrSavedReportNotFound", err)
	}
}

func TestSavedReportRepositoryScopesCostExplorerReportsByViewer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSavedReportRepository(db)

	managementPolicy, err := billingvisibility.PolicyForViewer(billingvisibility.Viewer{
		Role:      billingvisibility.RoleManagementAccount,
		AccountID: AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("PolicyForViewer(management) error = %v", err)
	}
	memberPolicy, err := billingvisibility.PolicyForViewer(billingvisibility.Viewer{
		Role:      billingvisibility.RoleMemberAccount,
		AccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("PolicyForViewer(member) error = %v", err)
	}
	if !managementPolicy.AllowsView(billingvisibility.ViewCostExplorer) ||
		!managementPolicy.AllowsView(billingvisibility.ViewExports) ||
		!memberPolicy.AllowsView(billingvisibility.ViewCostExplorer) ||
		!memberPolicy.AllowsView(billingvisibility.ViewExports) {
		t.Fatalf("viewer policies should allow Cost Explorer report and export surfaces: management=%+v member=%+v", managementPolicy, memberPolicy)
	}

	managementReport, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-management-visibility",
		Name:           "Monthly account spend",
		OwnerAccountID: AnyCompanyRetailManagementAccountID,
		OwnerRole:      managementPolicy.Role.String(),
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Filters: map[string][]string{
			"linked_account": {"111122223333", "222233334444"},
		},
		Groupings: []SavedReportGrouping{
			{Type: "dimension", Key: "linked_account"},
		},
	})
	if err != nil {
		t.Fatalf("Create(management report) error = %v", err)
	}
	memberReport, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-member-visibility",
		Name:           "Monthly account spend",
		OwnerAccountID: "111122223333",
		OwnerRole:      memberPolicy.Role.String(),
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Filters: map[string][]string{
			"linked_account": {"111122223333"},
		},
		Groupings: []SavedReportGrouping{
			{Type: "dimension", Key: "service"},
		},
	})
	if err != nil {
		t.Fatalf("Create(member report) error = %v", err)
	}
	otherMemberReport, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-other-member-visibility",
		Name:           "Monthly account spend",
		OwnerAccountID: "222233334444",
		OwnerRole:      memberPolicy.Role.String(),
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("Create(other member report) error = %v", err)
	}

	managementReports, err := repo.List(ctx, SavedReportListRequest{
		OwnerAccountID: AnyCompanyRetailManagementAccountID,
		OwnerRole:      managementPolicy.Role.String(),
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("List(management viewer) error = %v", err)
	}
	if len(managementReports) != 1 || managementReports[0].ID != managementReport.ID {
		t.Fatalf("management reports = %+v, want only management-owned Cost Explorer report", managementReports)
	}

	memberReports, err := repo.List(ctx, SavedReportListRequest{
		OwnerAccountID: "111122223333",
		OwnerRole:      memberPolicy.Role.String(),
		Limit:          10,
	})
	if err != nil {
		t.Fatalf("List(member viewer) error = %v", err)
	}
	if len(memberReports) != 1 || memberReports[0].ID != memberReport.ID {
		t.Fatalf("member reports = %+v, want only account-scoped member Cost Explorer report", memberReports)
	}

	memberByName, err := repo.GetByName(ctx, "111122223333", memberPolicy.Role.String(), "monthly ACCOUNT spend")
	if err != nil {
		t.Fatalf("GetByName(member viewer) error = %v", err)
	}
	if memberByName.ID != memberReport.ID || memberByName.ID == managementReport.ID || memberByName.ID == otherMemberReport.ID {
		t.Fatalf("member report by name = %+v, want same-name report owned by member account", memberByName)
	}
}

func TestSavedReportRepositoryAllowsSameOwnerAccountNameForDifferentRoles(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSavedReportRepository(db)

	managementReport, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-management-payer-shelf",
		Name:           "Shared payer report",
		OwnerAccountID: AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("Create(management report) error = %v", err)
	}
	financeReport, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-finance-payer-shelf",
		Name:           "Shared payer report",
		OwnerAccountID: AnyCompanyRetailManagementAccountID,
		OwnerRole:      "finance",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("Create(finance report) error = %v", err)
	}

	managementByName, err := repo.GetByName(ctx, AnyCompanyRetailManagementAccountID, "management-account", "shared PAYER report")
	if err != nil {
		t.Fatalf("GetByName(management) error = %v", err)
	}
	financeByName, err := repo.GetByName(ctx, AnyCompanyRetailManagementAccountID, "finance", "shared PAYER report")
	if err != nil {
		t.Fatalf("GetByName(finance) error = %v", err)
	}
	if managementByName.ID != managementReport.ID || financeByName.ID != financeReport.ID {
		t.Fatalf("role-scoped reports = management %+v finance %+v, want distinct role shelves", managementByName, financeByName)
	}

	if _, err := repo.Create(ctx, SavedReportCreateRequest{
		ID:             "saved-report-finance-payer-shelf-duplicate",
		Name:           "shared payer REPORT",
		OwnerAccountID: AnyCompanyRetailManagementAccountID,
		OwnerRole:      "finance",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	}); err == nil {
		t.Fatal("Create(duplicate finance report) error = nil, want same role/account/name uniqueness failure")
	}
}

func TestSavedReportRepositoryValidatesDefinitionsAndRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSavedReportRepository(db)

	valid := SavedReportCreateRequest{
		ID:             "saved-report-validation",
		Name:           "Validation report",
		OwnerAccountID: "999988887777",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	}
	if _, err := repo.Create(ctx, valid); err != nil {
		t.Fatalf("Create(valid) error = %v", err)
	}

	invalidCases := []struct {
		name    string
		request SavedReportCreateRequest
		want    string
	}{
		{
			name:    "blank name",
			request: SavedReportCreateRequest{OwnerAccountID: "999988887777", DateRangeStart: "2026-02-01", DateRangeEnd: "2026-03-01"},
			want:    "name is required",
		},
		{
			name:    "blank owner",
			request: SavedReportCreateRequest{Name: "Spend", DateRangeStart: "2026-02-01", DateRangeEnd: "2026-03-01"},
			want:    "owner account ID is required",
		},
		{
			name:    "bad date range",
			request: SavedReportCreateRequest{Name: "Spend", OwnerAccountID: "999988887777", DateRangeStart: "2026-03-01", DateRangeEnd: "2026-02-01"},
			want:    "start must be before end",
		},
		{
			name:    "too many groupings",
			request: SavedReportCreateRequest{Name: "Spend", OwnerAccountID: "999988887777", DateRangeStart: "2026-02-01", DateRangeEnd: "2026-03-01", Groupings: []SavedReportGrouping{{Type: "dimension", Key: "service"}, {Type: "tag", Key: "app"}, {Type: "cost_category", Key: "team"}}},
			want:    "at most two groupings",
		},
		{
			name:    "unsupported metric",
			request: SavedReportCreateRequest{Name: "Spend", OwnerAccountID: "999988887777", DateRangeStart: "2026-02-01", DateRangeEnd: "2026-03-01", Metrics: []string{"cash_cost"}},
			want:    "metric",
		},
	}
	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := repo.Create(ctx, tc.request); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Create() error = %v, want %q", err, tc.want)
			}
		})
	}

	if _, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:       valid.ID,
		RunAt:    "not-a-time",
		Status:   savedReportStatusSucceeded,
		RowCount: 1,
	}); err == nil || !strings.Contains(err.Error(), "must use RFC3339") {
		t.Fatalf("RecordLastRun(invalid time) error = %v, want RFC3339 validation", err)
	}
	if _, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:       valid.ID,
		RunAt:    "2026-02-01T00:00:00Z",
		Status:   savedReportStatusNeverRun,
		RowCount: 1,
	}); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("RecordLastRun(never_run) error = %v, want unsupported status", err)
	}
	if _, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:       valid.ID,
		RunAt:    "2026-02-01T00:00:00Z",
		Status:   savedReportStatusFailed,
		RowCount: -1,
	}); err == nil || !strings.Contains(err.Error(), "row count") {
		t.Fatalf("RecordLastRun(negative rows) error = %v, want row-count validation", err)
	}
	if _, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:                valid.ID,
		RunAt:             "2026-02-01T00:00:00Z",
		Status:            savedReportStatusSucceeded,
		Metric:            "cash_cost",
		MetricTotalMicros: 1,
	}); err == nil || !strings.Contains(err.Error(), "metric") {
		t.Fatalf("RecordLastRun(unsupported metric) error = %v, want metric validation", err)
	}
	if _, err := repo.RecordLastRun(ctx, SavedReportRunUpdate{
		ID:                valid.ID,
		RunAt:             "2026-02-01T00:00:00Z",
		Status:            savedReportStatusSucceeded,
		Metric:            "usage_quantity",
		MetricTotalMicros: -1,
	}); err == nil || !strings.Contains(err.Error(), "usage quantity total") {
		t.Fatalf("RecordLastRun(negative usage quantity) error = %v, want usage metric validation", err)
	}
	if _, err := repo.Update(ctx, SavedReportUpdateRequest{
		Name:           "Missing ID",
		OwnerAccountID: "999988887777",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	}); err == nil || !strings.Contains(err.Error(), "ID is required") {
		t.Fatalf("Update(blank ID) error = %v, want ID validation", err)
	}
}

func TestSavedReportSchemaRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	db := openTestWorkspace(t)

	assertExecFails(t, db, `INSERT INTO saved_reports (
		id,
		name,
		owner_account_id,
		owner_role,
		date_range_start,
		date_range_end,
		granularity,
		groupings_json,
		metrics_json,
		chart_type
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"saved-report-too-many-groupings",
		"Too many groupings",
		"999988887777",
		"management-account",
		"2026-02-01",
		"2026-03-01",
		"monthly",
		`[{"type":"dimension","key":"service"},{"type":"tag","key":"app"},{"type":"tag","key":"env"}]`,
		`["unblended_cost"]`,
		"table",
	)
	assertExecFails(t, db, `INSERT INTO saved_reports (
		id,
		name,
		owner_account_id,
		owner_role,
		date_range_start,
		date_range_end,
		granularity,
		filters_json,
		metrics_json,
		chart_type
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"saved-report-invalid-json",
		"Invalid JSON",
		"999988887777",
		"management-account",
		"2026-02-01",
		"2026-03-01",
		"monthly",
		`["not-an-object"]`,
		`["unblended_cost"]`,
		"table",
	)
	assertExecFails(t, db, `INSERT INTO saved_reports (
		id,
		name,
		owner_account_id,
		owner_role,
		date_range_start,
		date_range_end,
		granularity,
		metrics_json,
		chart_type,
		last_run_status
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"saved-report-run-without-time",
		"Run without time",
		"999988887777",
		"management-account",
		"2026-02-01",
		"2026-03-01",
		"monthly",
		`["unblended_cost"]`,
		"table",
		savedReportStatusSucceeded,
	)
	assertExecFails(t, db, `INSERT INTO saved_reports (
		id,
		name,
		owner_account_id,
		owner_role,
		date_range_start,
		date_range_end,
		granularity,
		metrics_json,
		chart_type,
		last_run_metric
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"saved-report-invalid-run-metric",
		"Invalid run metric",
		"999988887777",
		"management-account",
		"2026-02-01",
		"2026-03-01",
		"monthly",
		`["unblended_cost"]`,
		"table",
		"cash_cost",
	)
}
