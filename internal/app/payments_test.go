package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestPaymentsUIResolvesFailedInvoiceAndProfileMethod(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "payments-ui-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-payments-ui-web",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Payments UI web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-payments-ui-web",
		ResourceID:          "resource-payments-ui-web",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T03:00:00Z",
		UsageQuantityMicros: 3_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	closeResult, err := persistence.NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		InvoiceDueDays: 14,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	profileRepo := persistence.NewPaymentProfileRepository(db)
	failedMethod, err := profileRepo.CreatePaymentMethod(ctx, persistence.PaymentMethodCreateRequest{
		ID:               "paymeth_ui_failed_card",
		PaymentProfileID: "payprof_anycompany_retail_management",
		MethodType:       "card",
		DisplayName:      "Expired corporate card",
		Status:           "failed",
		CardBrand:        "Visa",
		AccountLast4:     "4242",
		ExpirationMonth:  2,
		ExpirationYear:   2026,
		FailureReason:    "card expired",
	})
	if err != nil {
		t.Fatalf("CreatePaymentMethod(failed card) error = %v", err)
	}
	if _, err := profileRepo.CreatePaymentMethod(ctx, persistence.PaymentMethodCreateRequest{
		ID:                      "paymeth_ui_advance_pay",
		PaymentProfileID:        "payprof_anycompany_retail_management",
		MethodType:              "advance_pay_balance",
		DisplayName:             "Advance Pay reserve",
		AdvancePayBalanceMicros: 3_500_000,
	}); err != nil {
		t.Fatalf("CreatePaymentMethod(Advance Pay) error = %v", err)
	}

	client := appTestHTTPClient()
	resp, err := client.Get(server.URL() + "/payments")
	if err != nil {
		t.Fatalf("GET /payments error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /payments status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Payments - Billing Simulator</title>`,
		`href="/payments">Payments</a>`,
		"Due Invoices",
		"Payment History",
		"Payment Setup",
		closeResult.InvoiceObligation.InvoiceID,
		"due",
		"Invoice remittance",
		"Expired corporate card",
		"card expired",
		"Advance Pay reserve",
		"$3.50",
		`action="/payments/action"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /payments body missing %q: %s", want, body)
		}
	}

	obligationID := closeResult.InvoiceObligation.ID
	financePaymentPath := "/payments?viewer_role=finance"
	resp, err = client.Get(server.URL() + financePaymentPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", financePaymentPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", financePaymentPath, resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="finance" selected>Finance</option>`,
		`href="` + invoicePathForIDWithViewer(closeResult.InvoiceObligation.InvoiceID, exportViewerFields{Role: "finance"}) + `"`,
		`name="viewer_role" value="finance"`,
		closeResult.InvoiceObligation.InvoiceID,
		"Payment Setup",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET %s body missing %q: %s", financePaymentPath, want, body)
		}
	}

	memberPaymentPath := "/payments?viewer_role=member-account&viewer_account_id=111122223333"
	resp, err = client.Get(server.URL() + memberPaymentPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", memberPaymentPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET %s status = %d, want %d; body=%s", memberPaymentPath, resp.StatusCode, http.StatusForbidden, body)
	}
	if !strings.Contains(body, "cannot manage payments") ||
		strings.Contains(body, closeResult.InvoiceObligation.InvoiceID) ||
		strings.Contains(body, `action="/payments/action"`) {
		t.Fatalf("GET %s did not block member payment workflow cleanly: %s", memberPaymentPath, body)
	}

	assertObligationStatus := func(want string) {
		t.Helper()
		var got string
		if err := db.QueryRowContext(ctx, `SELECT status FROM invoice_payment_states WHERE invoice_obligation_id = ?`, obligationID).Scan(&got); err != nil {
			t.Fatalf("read invoice payment status: %v", err)
		}
		if got != want {
			t.Fatalf("invoice payment status = %q, want %q", got, want)
		}
	}
	resp, err = client.PostForm(server.URL()+"/payments/action", url.Values{
		"viewer_role":           {"member-account"},
		"viewer_account_id":     {"111122223333"},
		"invoice_obligation_id": {obligationID},
		"action":                {"schedule"},
	})
	if err != nil {
		t.Fatalf("POST /payments/action member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /payments/action member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	assertObligationStatus("due")

	resp, err = client.PostForm(server.URL()+"/payments/action", url.Values{
		"viewer_role":           {"management-account"},
		"viewer_account_id":     {"000000000000"},
		"invoice_obligation_id": {obligationID},
		"action":                {"schedule"},
	})
	if err != nil {
		t.Fatalf("POST /payments/action cross-payer viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /payments/action cross-payer viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	assertObligationStatus("due")

	postPaymentAction := func(values url.Values) string {
		t.Helper()
		resp, err := client.PostForm(server.URL()+"/payments/action", values)
		if err != nil {
			t.Fatalf("POST /payments/action error = %v", err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /payments/action final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
		}
		return body
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"schedule"},
	})
	if !strings.Contains(body, "Scheduled payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "scheduled") {
		t.Fatalf("schedule payment response missing scheduled state: %s", body)
	}
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"process"},
	})
	if !strings.Contains(body, "Started payment processing for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "processing") {
		t.Fatalf("process payment response missing processing state: %s", body)
	}
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"fail"},
		"reason":                {"card expired"},
	})
	if !strings.Contains(body, "Recorded failed payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "failed") ||
		!strings.Contains(body, "card expired") {
		t.Fatalf("fail payment response missing failed reason: %s", body)
	}
	body = postPaymentAction(url.Values{
		"method_id": {failedMethod.ID},
		"action":    {"fix_method"},
	})
	if !strings.Contains(body, "Fixed payment method Expired corporate card") ||
		!strings.Contains(body, "active") {
		t.Fatalf("fix method response missing active method: %s", body)
	}

	var methodStatus, failureReason string
	if err := db.QueryRowContext(ctx, `SELECT status, failure_reason FROM payment_methods WHERE id = ?`, failedMethod.ID).Scan(&methodStatus, &failureReason); err != nil {
		t.Fatalf("read fixed method: %v", err)
	}
	if methodStatus != "active" || failureReason != "" {
		t.Fatalf("fixed method state = %q/%q, want active with no failure", methodStatus, failureReason)
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"mark_past_due"},
		"occurred_at":           {"2026-03-23"},
	})
	if !strings.Contains(body, "Marked "+closeResult.InvoiceObligation.InvoiceID+" past due") ||
		!strings.Contains(body, "past-due") {
		t.Fatalf("mark past-due response missing past-due state: %s", body)
	}
	partialMicros := int64(500_000)
	remainingMicros := closeResult.InvoiceObligation.AmountDueMicros - partialMicros
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"collect"},
		"amount":                {formatMicrosDecimal(partialMicros)},
	})
	if !strings.Contains(body, "Collected payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "Partially Paid") ||
		!strings.Contains(body, "partially-paid") ||
		!strings.Contains(body, "past-due") ||
		!strings.Contains(body, `value="mark_due"`) ||
		!strings.Contains(body, formatUSDMicros(remainingMicros)) {
		t.Fatalf("partial past-due collect response missing partial and past-due state: %s", body)
	}

	var partialBillState, partialPaymentStatus string
	var partialAmountDue int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT b.bill_state, ps.status, ps.amount_due_micros
		   FROM bills b
		   JOIN invoice_payment_states ps ON ps.invoice_obligation_id = ?
		  WHERE b.id = ?`,
		obligationID,
		closeResult.Bill.ID,
	).Scan(&partialBillState, &partialPaymentStatus, &partialAmountDue); err != nil {
		t.Fatalf("read partial payment state: %v", err)
	}
	if partialBillState != "past_due" || partialPaymentStatus != "partially_paid" || partialAmountDue != remainingMicros {
		t.Fatalf("partial payment state = bill %q payment %q due %d, want past_due/partially_paid/%d", partialBillState, partialPaymentStatus, partialAmountDue, remainingMicros)
	}

	resp, err = client.Get(server.URL() + "/bills")
	if err != nil {
		t.Fatalf("GET /bills after partial past-due payment error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills after partial past-due payment status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "past-due") || !strings.Contains(body, "partially-paid") {
		t.Fatalf("GET /bills after partial past-due payment missing past-due partial invoice state: %s", body)
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"mark_due"},
	})
	if !strings.Contains(body, "Marked "+closeResult.InvoiceObligation.InvoiceID+" due") ||
		!strings.Contains(body, "due") ||
		!strings.Contains(body, formatUSDMicros(remainingMicros)) {
		t.Fatalf("mark partially paid due response missing due state: %s", body)
	}
	var dueBillState, duePaymentStatus string
	var dueAmountDue, dueAmountPaid int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT b.bill_state, ps.status, ps.amount_due_micros, ps.amount_paid_micros
		   FROM bills b
		   JOIN invoice_payment_states ps ON ps.invoice_obligation_id = ?
		  WHERE b.id = ?`,
		obligationID,
		closeResult.Bill.ID,
	).Scan(&dueBillState, &duePaymentStatus, &dueAmountDue, &dueAmountPaid); err != nil {
		t.Fatalf("read marked-due partial payment state: %v", err)
	}
	if dueBillState != "issued" || duePaymentStatus != "due" || dueAmountDue != remainingMicros || dueAmountPaid != partialMicros {
		t.Fatalf("marked-due partial payment state = bill %q payment %q due %d paid %d, want issued/due/%d/%d", dueBillState, duePaymentStatus, dueAmountDue, dueAmountPaid, remainingMicros, partialMicros)
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"process"},
	})
	if !strings.Contains(body, "processing") {
		t.Fatalf("retry after method fix response missing processing state: %s", body)
	}
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"collect"},
		"amount":                {formatMicrosDecimal(remainingMicros)},
	})
	if !strings.Contains(body, "Collected payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "succeeded") ||
		!strings.Contains(body, "Payment History") {
		t.Fatalf("collect payment response missing succeeded history: %s", body)
	}

	var billState, paymentStatus string
	if err := db.QueryRowContext(ctx, `SELECT bill_state FROM bills WHERE id = ?`, closeResult.Bill.ID).Scan(&billState); err != nil {
		t.Fatalf("read bill state: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM invoice_payment_states WHERE invoice_obligation_id = ?`, obligationID).Scan(&paymentStatus); err != nil {
		t.Fatalf("read payment state: %v", err)
	}
	if billState != "paid" || paymentStatus != "succeeded" {
		t.Fatalf("payment result = bill %q payment %q, want paid/succeeded", billState, paymentStatus)
	}
}
