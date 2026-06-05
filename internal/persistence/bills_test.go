package persistence

import (
	"context"
	"database/sql"
	"testing"
)

func TestBillsRepositoryListsOpenPendingAndStoredBillStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-15T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-bills-february",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-bills-february",
			ResourceID:          "resource-bills-february",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-bills-march",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-bills-march",
			ResourceID:          "resource-bills-march",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-03-02T00:00:00Z",
			UsageEndTime:        "2026-03-02T01:00:00Z",
			UsageQuantityMicros: 1_000_000,
			UsageUnit:           "Hours",
		},
	)

	insertStoredBillState(t, ctx, db, "2025-10-01", "2025-11-01", "111122223333", billStateIssued, invoiceObligationStatusDue, 1_000_000, 0, 0, 0)
	insertStoredBillState(t, ctx, db, "2025-11-01", "2025-12-01", "111122223333", "adjusted", invoiceObligationStatusDue, 3_000_000, 500_000, 0, 200_000)
	insertStoredBillState(t, ctx, db, "2025-12-01", "2026-01-01", "111122223333", "paid", "paid", 4_000_000, 0, 0, 0)
	insertStoredBillState(t, ctx, db, "2026-01-01", "2026-02-01", "111122223333", "past_due", "past_due", 5_000_000, 0, 0, 0)

	summaries, err := NewBillsRepository(db).ListBillStateSummaries(ctx, BillStateSummaryRequest{
		Limit:                 50,
		DefaultPayerAccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries() error = %v", err)
	}

	open := requireBillStateSummary(t, summaries, billStateOpen, "2026-03-01")
	if open.LineItemCount != 1 || open.UsageChargeMicros != 41_600 || open.TotalMicros != 41_600 {
		t.Fatalf("open summary = %+v, want current March estimated charge", open)
	}
	pending := requireBillStateSummary(t, summaries, billStatePendingClose, "2026-02-01")
	if pending.LineItemCount != 1 || pending.UsageChargeMicros != 83_200 || pending.TotalMicros != 83_200 {
		t.Fatalf("pending summary = %+v, want completed February estimated charge", pending)
	}
	issued := requireBillStateSummary(t, summaries, billStateIssued, "2025-10-01")
	if issued.InvoiceStatus != invoiceObligationStatusDue || issued.TotalMicros != 1_000_000 {
		t.Fatalf("issued summary = %+v, want due issued bill", issued)
	}
	adjusted := requireBillStateSummary(t, summaries, "adjusted", "2025-11-01")
	if adjusted.UsageChargeMicros != 3_000_000 || adjusted.CreditMicros != 500_000 || adjusted.TaxMicros != 200_000 || adjusted.TotalMicros != 2_700_000 {
		t.Fatalf("adjusted summary = %+v, want charges, credits, tax, and adjusted total", adjusted)
	}
	paid := requireBillStateSummary(t, summaries, "paid", "2025-12-01")
	if paid.InvoiceStatus != "paid" || paid.InvoiceAmountPaidMicros != 4_000_000 || paid.InvoiceAmountDueMicros != 0 {
		t.Fatalf("paid summary = %+v, want paid invoice obligation", paid)
	}
	pastDue := requireBillStateSummary(t, summaries, "past_due", "2026-01-01")
	if pastDue.InvoiceStatus != "past_due" || pastDue.TotalMicros != 5_000_000 {
		t.Fatalf("past-due summary = %+v, want past-due bill", pastDue)
	}
}

func TestBillsRepositoryAddsEmptyOpenSummaryForFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	summaries, err := NewBillsRepository(db).ListBillStateSummaries(ctx, BillStateSummaryRequest{
		DefaultPayerAccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries() error = %v", err)
	}
	open := requireBillStateSummary(t, summaries, billStateOpen, "2026-02-01")
	if open.LineItemCount != 0 || open.TotalMicros != 0 || open.PayerAccountID != "111122223333" {
		t.Fatalf("empty open summary = %+v, want zero-dollar current period", open)
	}
}

func TestBillsRepositoryListsChargeBreakdowns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	ec2Item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-charge-breakdown-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Checkout web",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-charge-breakdown-ec2",
			ResourceID:          "resource-charge-breakdown-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	s3Item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-charge-breakdown-s3",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Receipts bucket",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-charge-breakdown-s3",
			ResourceID:          "resource-charge-breakdown-s3",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)
	supportResult, err := NewSupportChargeRepository(db).GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
		PayerAccountID: ec2Item.PayerAccountID,
		PeriodStart:    "2026-02-01",
		PeriodEnd:      "2026-03-01",
		LineItemStatus: billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("GenerateSupportCharges() error = %v", err)
	}
	if supportResult.ItemsCreated != 1 || len(supportResult.Items) != 1 {
		t.Fatalf("GenerateSupportCharges() = %+v, want one support item", supportResult)
	}
	supportItem := supportResult.Items[0]

	breakdowns, err := NewBillsRepository(db).ListChargeBreakdowns(ctx, BillChargeBreakdownRequest{Limit: 50})
	if err != nil {
		t.Fatalf("ListChargeBreakdowns() error = %v", err)
	}

	ec2Summary := requireBillChargeSummary(t, breakdowns.Summaries, serviceAmazonEC2, AnyCompanyRetailManagementAccountID, "111122223333", "instance-hours:t3.medium")
	if ec2Summary.ServiceName != "Amazon EC2" ||
		ec2Summary.RegionCode != "us-east-1" ||
		ec2Summary.LineItemStatus != billLineItemStatusEstimated ||
		ec2Summary.LineItemCount != 1 ||
		ec2Summary.ResourceCount != 1 ||
		ec2Summary.ChargeMicros != ec2Item.UnblendedCostMicros ||
		ec2Summary.TotalMicros != ec2Item.UnblendedCostMicros {
		t.Fatalf("EC2 charge summary = %+v, want source line item total", ec2Summary)
	}
	s3Summary := requireBillChargeSummary(t, breakdowns.Summaries, serviceAmazonS3, AnyCompanyRetailManagementAccountID, "222233334444", "requests:put-1k")
	if s3Summary.ChargeMicros != s3Item.UnblendedCostMicros || s3Summary.TotalMicros != 7_500 {
		t.Fatalf("S3 charge summary = %+v, want PUT request total from account 222233334444", s3Summary)
	}
	supportSummary := requireBillChargeSummary(t, breakdowns.Summaries, serviceAWSSupport, AnyCompanyRetailManagementAccountID, AnyCompanyRetailManagementAccountID, supportBusinessUsageType)
	if supportSummary.RegionCode != supportRegionGlobal ||
		supportSummary.ResourceCount != 0 ||
		supportSummary.ChargeMicros != supportBusinessMinimumCostMicros ||
		supportSummary.TotalMicros != supportItem.UnblendedCostMicros {
		t.Fatalf("Support charge summary = %+v, want period-level fee total", supportSummary)
	}

	ec2Resource := requireBillResourceChargeSummary(t, breakdowns.Resources, "resource-charge-breakdown-ec2", serviceAmazonEC2)
	if ec2Resource.ResourceName != "Checkout web" ||
		ec2Resource.LineItemCount != 1 ||
		ec2Resource.TotalMicros != ec2Item.UnblendedCostMicros {
		t.Fatalf("EC2 resource drilldown = %+v, want named resource cost", ec2Resource)
	}
	supportResource := requireBillResourceChargeSummary(t, breakdowns.Resources, "", serviceAWSSupport)
	if supportResource.ResourceName != "" ||
		supportResource.Description == "" ||
		supportResource.TotalMicros != supportItem.UnblendedCostMicros {
		t.Fatalf("Support resource drilldown = %+v, want period-level support cost", supportResource)
	}
}

func TestBillsRepositoryReconcilesBillsToFinalLineItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-bill-reconciliation-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-bill-reconciliation-ec2",
			ResourceID:          "resource-bill-reconciliation-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	closeResult, err := NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	reconciliations, err := NewBillsRepository(db).ListBillReconciliations(ctx, BillReconciliationRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListBillReconciliations() error = %v", err)
	}
	balanced := requireBillReconciliation(t, reconciliations, closeResult.Bill.ID)
	if balanced.Status != billReconcileStatusBalanced ||
		balanced.BillLineItemCount != 2 ||
		balanced.SourceLineItemCount != 2 ||
		balanced.BillTotalMicros != 1_083_200 ||
		balanced.SourceTotalMicros != 1_083_200 ||
		balanced.TotalResidualMicros != 0 {
		t.Fatalf("balanced reconciliation = %+v, want bill total proven from final line items", balanced)
	}

	if _, err := db.ExecContext(ctx, `UPDATE bills SET total_micros = total_micros + 37 WHERE id = ?`, closeResult.Bill.ID); err != nil {
		t.Fatalf("add bill rounding residual: %v", err)
	}
	reconciliations, err = NewBillsRepository(db).ListBillReconciliations(ctx, BillReconciliationRequest{Limit: 10})
	if err != nil {
		t.Fatalf("ListBillReconciliations(residual) error = %v", err)
	}
	residual := requireBillReconciliation(t, reconciliations, closeResult.Bill.ID)
	if residual.Status != billReconcileStatusResidual ||
		residual.SourceTotalMicros != 1_083_200 ||
		residual.TotalResidualMicros != 37 ||
		residual.UsageChargeResidualMicros != 0 {
		t.Fatalf("residual reconciliation = %+v, want stored total residual against source lines", residual)
	}
}

func TestBillsRepositoryAppliesBillingVisibilityFilters(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	ec2Item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-visibility-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Visibility web",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-visibility-ec2",
			ResourceID:          "resource-visibility-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	s3Item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-visibility-s3",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Visibility bucket",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-visibility-s3",
			ResourceID:          "resource-visibility-s3",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	closeResult, err := NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	repo := NewBillsRepository(db)
	managementVisibility := BillingVisibilityFilter{PayerAccountID: AnyCompanyRetailManagementAccountID}
	memberVisibility := BillingVisibilityFilter{UsageAccountID: "111122223333"}

	managementSummaries, err := repo.ListBillStateSummaries(ctx, BillStateSummaryRequest{
		Limit:                 10,
		DefaultPayerAccountID: AnyCompanyRetailManagementAccountID,
		Visibility:            managementVisibility,
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries(management) error = %v", err)
	}
	managementIssued := requireBillStateSummary(t, managementSummaries, billStateIssued, "2026-02-01")
	if managementIssued.LineItemCount != 3 ||
		managementIssued.TotalMicros != ec2Item.UnblendedCostMicros+s3Item.UnblendedCostMicros+supportBusinessMinimumCostMicros ||
		managementIssued.InvoiceID == "" {
		t.Fatalf("management issued summary = %+v, want consolidated financial bill", managementIssued)
	}

	memberSummaries, err := repo.ListBillStateSummaries(ctx, BillStateSummaryRequest{
		Limit:                 10,
		DefaultPayerAccountID: AnyCompanyRetailManagementAccountID,
		Visibility:            memberVisibility,
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries(member) error = %v", err)
	}
	memberIssued := requireBillStateSummary(t, memberSummaries, billStateIssued, "2026-02-01")
	if memberIssued.LineItemCount != 1 ||
		memberIssued.TotalMicros != ec2Item.UnblendedCostMicros ||
		memberIssued.InvoiceID != "" {
		t.Fatalf("member issued summary = %+v, want informational usage-account total without invoice", memberIssued)
	}

	memberBreakdowns, err := repo.ListChargeBreakdowns(ctx, BillChargeBreakdownRequest{
		Limit:      10,
		Visibility: memberVisibility,
	})
	if err != nil {
		t.Fatalf("ListChargeBreakdowns(member) error = %v", err)
	}
	requireBillChargeSummary(t, memberBreakdowns.Summaries, serviceAmazonEC2, AnyCompanyRetailManagementAccountID, "111122223333", "instance-hours:t3.medium")
	for _, summary := range memberBreakdowns.Summaries {
		if summary.UsageAccountID != "111122223333" {
			t.Fatalf("member charge summary = %+v, want only usage account 111122223333", summary)
		}
	}

	memberReconciliations, err := repo.ListBillReconciliations(ctx, BillReconciliationRequest{
		Limit:      10,
		Visibility: memberVisibility,
	})
	if err != nil {
		t.Fatalf("ListBillReconciliations(member) error = %v", err)
	}
	if len(memberReconciliations) != 0 {
		t.Fatalf("member reconciliations = %+v, want no financial bill reconciliation rows", memberReconciliations)
	}
	managementReconciliations, err := repo.ListBillReconciliations(ctx, BillReconciliationRequest{
		Limit:      10,
		Visibility: managementVisibility,
	})
	if err != nil {
		t.Fatalf("ListBillReconciliations(management) error = %v", err)
	}
	requireBillReconciliation(t, managementReconciliations, closeResult.Bill.ID)
}

func TestBillsRepositoryRejectsNilDB(t *testing.T) {
	t.Parallel()

	if _, err := NewBillsRepository(nil).ListBillStateSummaries(context.Background(), BillStateSummaryRequest{}); err == nil {
		t.Fatal("ListBillStateSummaries(nil DB) error = nil, want database handle validation error")
	}
	if _, err := NewBillsRepository(nil).ListChargeBreakdowns(context.Background(), BillChargeBreakdownRequest{}); err == nil {
		t.Fatal("ListChargeBreakdowns(nil DB) error = nil, want database handle validation error")
	}
	if _, err := NewBillsRepository(nil).ListBillReconciliations(context.Background(), BillReconciliationRequest{}); err == nil {
		t.Fatal("ListBillReconciliations(nil DB) error = nil, want database handle validation error")
	}
}

func insertStoredBillState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	periodStart string,
	periodEnd string,
	payerAccountID string,
	billState string,
	invoiceStatus string,
	usageChargeMicros int64,
	creditMicros int64,
	refundMicros int64,
	taxMicros int64,
) {
	t.Helper()

	totalMicros := usageChargeMicros + taxMicros - creditMicros - refundMicros
	if totalMicros < 0 {
		totalMicros = 0
	}
	close := BillingPeriodClose{
		ID:                     billingPeriodCloseID(periodStart, periodEnd, payerAccountID),
		BillingPeriodStart:     periodStart,
		BillingPeriodEnd:       periodEnd,
		PayerAccountID:         payerAccountID,
		Status:                 billingPeriodCloseStatusClosed,
		FinalizedLineItemCount: 1,
		FinalizedCostMicros:    totalMicros,
		CurrencyCode:           defaultBillCurrencyCode,
	}
	bill := Bill{
		ID:                 billID(periodStart, periodEnd, payerAccountID, defaultBillCurrencyCode),
		CloseID:            close.ID,
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
		PayerAccountID:     payerAccountID,
		BillState:          billState,
		CurrencyCode:       defaultBillCurrencyCode,
		LineItemCount:      1,
		UsageChargeMicros:  usageChargeMicros,
		CreditMicros:       creditMicros,
		RefundMicros:       refundMicros,
		TaxMicros:          taxMicros,
		TotalMicros:        totalMicros,
	}
	obligation := invoiceObligationFromBill(bill, defaultInvoiceObligationDueDay)
	obligation.Status = invoiceStatus
	if invoiceStatus == "paid" {
		obligation.AmountPaidMicros = totalMicros
		obligation.AmountDueMicros = 0
	}

	if err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		if err := insertBillingPeriodClose(ctx, tx, close); err != nil {
			return err
		}
		if err := insertBill(ctx, tx, bill); err != nil {
			return err
		}
		return insertInvoiceObligation(ctx, tx, obligation)
	}); err != nil {
		t.Fatalf("insert stored bill state %s: %v", billState, err)
	}
}

func requireBillStateSummary(t *testing.T, summaries []BillStateSummary, state, periodStart string) BillStateSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.BillState == state && summary.BillingPeriodStart == periodStart {
			return summary
		}
	}
	t.Fatalf("summaries = %+v, want state %s for period %s", summaries, state, periodStart)
	return BillStateSummary{}
}

func requireBillChargeSummary(t *testing.T, summaries []BillChargeSummary, serviceCode, payerAccountID, usageAccountID, usageType string) BillChargeSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.ServiceCode == serviceCode &&
			summary.PayerAccountID == payerAccountID &&
			summary.UsageAccountID == usageAccountID &&
			summary.UsageType == usageType {
			return summary
		}
	}
	t.Fatalf("charge summaries = %+v, want service %s payer %s usage account %s usage type %s", summaries, serviceCode, payerAccountID, usageAccountID, usageType)
	return BillChargeSummary{}
}

func requireBillResourceChargeSummary(t *testing.T, summaries []BillResourceChargeSummary, resourceID, serviceCode string) BillResourceChargeSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.ResourceID == resourceID && summary.ServiceCode == serviceCode {
			return summary
		}
	}
	t.Fatalf("resource charge summaries = %+v, want resource %q service %s", summaries, resourceID, serviceCode)
	return BillResourceChargeSummary{}
}

func requireBillReconciliation(t *testing.T, reconciliations []BillReconciliation, billID string) BillReconciliation {
	t.Helper()

	for _, reconciliation := range reconciliations {
		if reconciliation.BillID == billID {
			return reconciliation
		}
	}
	t.Fatalf("bill reconciliations = %+v, want bill %s", reconciliations, billID)
	return BillReconciliation{}
}
