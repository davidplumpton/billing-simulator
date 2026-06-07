package scenario

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

const (
	// CheckStatusPassed marks an assessment check whose observed evidence matched expectations.
	CheckStatusPassed = "passed"

	// CheckStatusFailed marks an assessment check whose observed evidence did not match expectations.
	CheckStatusFailed = "failed"
)

// CheckEvaluationResult summarizes all assessment checks for one scenario definition.
type CheckEvaluationResult struct {
	DefinitionName string
	ChecksTotal    int
	ChecksPassed   int
	ChecksFailed   int
	Results        []CheckEvaluation
}

// CheckEvaluation records the evidence for one scenario assessment check.
type CheckEvaluation struct {
	ID       string
	Sequence int
	Type     CheckType
	Status   string
	Passed   bool
	Expected string
	Actual   string
	Message  string
}

// Evaluator reads a workspace database and evaluates scenario assessment checks.
type Evaluator struct {
	db         *sql.DB
	tags       persistence.CostAllocationTagRepository
	categories persistence.CostCategoryRepository
	bills      persistence.BillsRepository
	monthEnd   persistence.MonthEndCloseRepository
}

// NewEvaluator creates a scenario check evaluator backed by the workspace database.
func NewEvaluator(db *sql.DB) Evaluator {
	return Evaluator{
		db:         db,
		tags:       persistence.NewCostAllocationTagRepository(db),
		categories: persistence.NewCostCategoryRepository(db),
		bills:      persistence.NewBillsRepository(db),
		monthEnd:   persistence.NewMonthEndCloseRepository(db),
	}
}

// Evaluate runs every check in definition order and returns pass/fail evidence.
func (e Evaluator) Evaluate(ctx context.Context, definition Definition) (CheckEvaluationResult, error) {
	if e.db == nil {
		return CheckEvaluationResult{}, fmt.Errorf("database handle is required")
	}
	definition, err := normalizeAndValidate(definition)
	if err != nil {
		return CheckEvaluationResult{}, err
	}
	result := CheckEvaluationResult{
		DefinitionName: definition.Name,
		ChecksTotal:    len(definition.Checks),
		Results:        make([]CheckEvaluation, 0, len(definition.Checks)),
	}
	for _, check := range definition.Checks {
		evaluation, err := e.evaluateCheck(ctx, definition, check)
		if err != nil {
			return CheckEvaluationResult{}, err
		}
		if evaluation.Passed {
			result.ChecksPassed++
		} else {
			result.ChecksFailed++
		}
		result.Results = append(result.Results, evaluation)
	}
	return result, nil
}

func (e Evaluator) evaluateCheck(ctx context.Context, definition Definition, check Check) (CheckEvaluation, error) {
	switch check.Type {
	case CheckTypeSavedReportExists:
		return e.evaluateSavedReportExists(ctx, definition, check)
	case CheckTypeIdentifiesTopDriver:
		return e.evaluateIdentifiesTopDriver(ctx, definition, check)
	case CheckTypeCostAllocationTagActivated:
		return e.evaluateCostAllocationTagActivated(ctx, check)
	case CheckTypeCostCategoryRuleCreated:
		return e.evaluateCostCategoryRuleCreated(ctx, definition, check)
	case CheckTypeBillReconciled:
		return e.evaluateBillReconciled(ctx, definition, check)
	case CheckTypePaymentStatus:
		return e.evaluatePaymentStatus(ctx, definition, check)
	default:
		return CheckEvaluation{}, fmt.Errorf("scenario check type %q is not executable", check.Type)
	}
}

func (e Evaluator) evaluateSavedReportExists(ctx context.Context, definition Definition, check Check) (CheckEvaluation, error) {
	ownerAccountID, err := e.resolveAccountID(ctx, definition, check.AccountID, check.Account)
	if err != nil {
		return CheckEvaluation{}, err
	}
	clauses := []string{"lower(name) = lower(?)"}
	args := []any{check.ReportName}
	if ownerAccountID != "" {
		clauses = append(clauses, "owner_account_id = ?")
		args = append(args, ownerAccountID)
	}
	var reportID, ownerID string
	err = e.db.QueryRowContext(
		ctx,
		`SELECT id, owner_account_id
		 FROM saved_reports
		 WHERE `+strings.Join(clauses, " AND ")+`
		 ORDER BY updated_at DESC, id
		 LIMIT 1`,
		args...,
	).Scan(&reportID, &ownerID)
	evaluation := baseCheckEvaluation(check, check.ReportName)
	if err == sql.ErrNoRows {
		evaluation.Actual = "not found"
		evaluation.Message = fmt.Sprintf("saved report %q was not found", check.ReportName)
		return finishCheckEvaluation(evaluation, false), nil
	}
	if err != nil {
		return CheckEvaluation{}, fmt.Errorf("evaluate saved report check %q: %w", check.ID, err)
	}
	evaluation.Actual = fmt.Sprintf("%s owner=%s", reportID, ownerID)
	evaluation.Message = fmt.Sprintf("saved report %q exists", check.ReportName)
	return finishCheckEvaluation(evaluation, true), nil
}

func (e Evaluator) evaluateIdentifiesTopDriver(ctx context.Context, definition Definition, check Check) (CheckEvaluation, error) {
	accountID, err := e.resolveAccountID(ctx, definition, check.AccountID, check.Account)
	if err != nil {
		return CheckEvaluation{}, err
	}
	whereSQL, args := billLineItemCheckFilters(accountID, check)
	rows, err := e.db.QueryContext(
		ctx,
		`SELECT service_code,
		        service_name,
		        COUNT(*) AS line_item_count,
		        COALESCE(SUM(unblended_cost_micros), 0) AS cost_micros
		 FROM bill_line_items
		 WHERE `+whereSQL+`
		 GROUP BY service_code, service_name
		 ORDER BY cost_micros DESC, line_item_count DESC, lower(service_name), service_code
		 LIMIT 1`,
		args...,
	)
	if err != nil {
		return CheckEvaluation{}, fmt.Errorf("evaluate top-driver check %q: %w", check.ID, err)
	}
	defer rows.Close()

	evaluation := baseCheckEvaluation(check, check.ExpectedService)
	var driver topDriver
	if rows.Next() {
		if err := rows.Scan(&driver.ServiceCode, &driver.ServiceName, &driver.LineItemCount, &driver.CostMicros); err != nil {
			return CheckEvaluation{}, fmt.Errorf("scan top-driver check %q: %w", check.ID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return CheckEvaluation{}, fmt.Errorf("iterate top-driver check %q: %w", check.ID, err)
	}
	if driver.ServiceCode == "" {
		evaluation.Actual = "no bill line items"
		evaluation.Message = "no bill line items matched the check filters"
		return finishCheckEvaluation(evaluation, false), nil
	}
	evaluation.Actual = fmt.Sprintf("%s (%s) cost_micros=%d", driver.ServiceName, driver.ServiceCode, driver.CostMicros)
	if checkServiceMatches(check.ExpectedService, driver.ServiceCode, driver.ServiceName) {
		evaluation.Message = fmt.Sprintf("%q is the top cost driver", driver.ServiceName)
		return finishCheckEvaluation(evaluation, true), nil
	}
	evaluation.Message = fmt.Sprintf("%q is the top cost driver, not %q", driver.ServiceName, check.ExpectedService)
	return finishCheckEvaluation(evaluation, false), nil
}

func (e Evaluator) evaluateCostAllocationTagActivated(ctx context.Context, check Check) (CheckEvaluation, error) {
	expectedStatus := normalizedCheckStatus(chooseFirst(check.Status, "active"))
	keys, err := e.tags.ListDiscoveredKeys(ctx)
	if err != nil {
		return CheckEvaluation{}, err
	}
	evaluation := baseCheckEvaluation(check, fmt.Sprintf("%s status=%s", check.TagKey, expectedStatus))
	for _, key := range keys {
		if key.Key != check.TagKey {
			continue
		}
		actualStatus := normalizedCheckStatus(key.ActivationStatus)
		evaluation.Actual = fmt.Sprintf("%s status=%s", key.Key, actualStatus)
		if statusMatches(expectedStatus, actualStatus) {
			evaluation.Message = fmt.Sprintf("cost allocation tag %q is %s", check.TagKey, actualStatus)
			return finishCheckEvaluation(evaluation, true), nil
		}
		evaluation.Message = fmt.Sprintf("cost allocation tag %q is %s, not %s", check.TagKey, actualStatus, expectedStatus)
		return finishCheckEvaluation(evaluation, false), nil
	}
	evaluation.Actual = "not found"
	evaluation.Message = fmt.Sprintf("cost allocation tag %q was not found", check.TagKey)
	return finishCheckEvaluation(evaluation, false), nil
}

func (e Evaluator) evaluateCostCategoryRuleCreated(ctx context.Context, definition Definition, check Check) (CheckEvaluation, error) {
	accountID, err := e.resolveAccountID(ctx, definition, check.AccountID, check.Account)
	if err != nil {
		return CheckEvaluation{}, err
	}
	category, err := e.categories.GetCategoryByName(ctx, check.Category)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no rows") {
			evaluation := baseCheckEvaluation(check, check.Category)
			evaluation.Actual = "not found"
			evaluation.Message = fmt.Sprintf("cost category %q was not found", check.Category)
			return finishCheckEvaluation(evaluation, false), nil
		}
		return CheckEvaluation{}, fmt.Errorf("evaluate cost category check %q: %w", check.ID, err)
	}
	rules, err := e.categories.ListRules(ctx, category.ID)
	if err != nil {
		return CheckEvaluation{}, fmt.Errorf("evaluate cost category rules check %q: %w", check.ID, err)
	}
	evaluation := baseCheckEvaluation(check, costCategoryRuleExpectation(check))
	for _, rule := range rules {
		if costCategoryRuleMatchesCheck(rule, check, accountID) {
			evaluation.Actual = fmt.Sprintf("%s rule=%s value=%s", category.Name, rule.ID, rule.Value)
			evaluation.Message = fmt.Sprintf("cost category %q has a matching rule", category.Name)
			return finishCheckEvaluation(evaluation, true), nil
		}
	}
	evaluation.Actual = fmt.Sprintf("%s rule_count=%d", category.Name, len(rules))
	evaluation.Message = fmt.Sprintf("cost category %q has no matching rule", category.Name)
	return finishCheckEvaluation(evaluation, false), nil
}

func (e Evaluator) evaluateBillReconciled(ctx context.Context, definition Definition, check Check) (CheckEvaluation, error) {
	payerAccountID, err := e.resolveCheckPayerAccountID(ctx, definition, check)
	if err != nil {
		return CheckEvaluation{}, err
	}
	expectedStatus := normalizedCheckStatus(chooseFirst(check.Status, "balanced"))
	rows, err := e.bills.ListBillReconciliations(ctx, persistence.BillReconciliationRequest{
		Limit: 100,
		Visibility: persistence.BillingVisibilityFilter{
			PayerAccountID: payerAccountID,
		},
	})
	if err != nil {
		return CheckEvaluation{}, fmt.Errorf("evaluate bill reconciliation check %q: %w", check.ID, err)
	}
	evaluation := baseCheckEvaluation(check, billReconciliationExpectation(check, payerAccountID, expectedStatus))
	for _, row := range rows {
		if !checkPeriodMatches(check, row.BillingPeriodStart, row.BillingPeriodEnd) {
			continue
		}
		actualStatus := normalizedCheckStatus(row.Status)
		evaluation.Actual = fmt.Sprintf("bill=%s status=%s residual_micros=%d", row.BillID, actualStatus, row.TotalResidualMicros)
		if statusMatches(expectedStatus, actualStatus) {
			evaluation.Message = fmt.Sprintf("bill %q reconciliation is %s", row.BillID, actualStatus)
			return finishCheckEvaluation(evaluation, true), nil
		}
		evaluation.Message = fmt.Sprintf("bill %q reconciliation is %s, not %s", row.BillID, actualStatus, expectedStatus)
		return finishCheckEvaluation(evaluation, false), nil
	}
	evaluation.Actual = "not found"
	evaluation.Message = "no bill reconciliation rows matched the check filters"
	return finishCheckEvaluation(evaluation, false), nil
}

func (e Evaluator) evaluatePaymentStatus(ctx context.Context, definition Definition, check Check) (CheckEvaluation, error) {
	payerAccountID, err := e.resolveCheckPayerAccountID(ctx, definition, check)
	if err != nil {
		return CheckEvaluation{}, err
	}
	expectedStatus := normalizedCheckStatus(check.Status)
	rows, err := e.monthEnd.ListIssuedBills(ctx, 50)
	if err != nil {
		return CheckEvaluation{}, fmt.Errorf("evaluate payment status check %q: %w", check.ID, err)
	}
	evaluation := baseCheckEvaluation(check, paymentStatusExpectation(check, payerAccountID, expectedStatus))
	for _, row := range rows {
		if payerAccountID != "" && row.Bill.PayerAccountID != payerAccountID {
			continue
		}
		if !checkPeriodMatches(check, row.Bill.BillingPeriodStart, row.Bill.BillingPeriodEnd) {
			continue
		}
		actualStatus := normalizedCheckStatus(row.Obligation.Status)
		evaluation.Actual = fmt.Sprintf("invoice=%s status=%s amount_due_micros=%d", row.Obligation.InvoiceID, actualStatus, row.Obligation.AmountDueMicros)
		if statusMatches(expectedStatus, actualStatus) {
			evaluation.Message = fmt.Sprintf("invoice %q payment status is %s", row.Obligation.InvoiceID, actualStatus)
			return finishCheckEvaluation(evaluation, true), nil
		}
		evaluation.Message = fmt.Sprintf("invoice %q payment status is %s, not %s", row.Obligation.InvoiceID, actualStatus, expectedStatus)
		return finishCheckEvaluation(evaluation, false), nil
	}
	evaluation.Actual = "not found"
	evaluation.Message = "no invoice obligations matched the check filters"
	return finishCheckEvaluation(evaluation, false), nil
}

func (e Evaluator) resolveCheckPayerAccountID(ctx context.Context, definition Definition, check Check) (string, error) {
	return e.resolveAccountID(
		ctx,
		definition,
		chooseFirst(check.PayerAccountID, check.AccountID),
		chooseFirst(check.PayerAccount, check.Account),
	)
}

func (e Evaluator) resolveAccountID(ctx context.Context, definition Definition, explicitID, name string) (string, error) {
	explicitID = strings.TrimSpace(explicitID)
	if explicitID != "" {
		return explicitID, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	args := []any{name, name}
	templateSQL := ""
	if definition.OrganizationTemplate != "" {
		templateSQL = " AND o.template_key = ?"
		args = append(args, definition.OrganizationTemplate)
	}
	var accountID string
	err := e.db.QueryRowContext(
		ctx,
		`SELECT a.id
		 FROM organization_account_hierarchy a
		 JOIN organizations o ON o.id = a.organization_id
		 WHERE (a.id = ? OR lower(a.name) = lower(?))`+templateSQL+`
		 ORDER BY a.sort_order, a.id
		 LIMIT 1`,
		args...,
	).Scan(&accountID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("scenario check account %q was not found", name)
	}
	if err != nil {
		return "", fmt.Errorf("resolve scenario check account %q: %w", name, err)
	}
	return accountID, nil
}

type topDriver struct {
	ServiceCode   string
	ServiceName   string
	LineItemCount int
	CostMicros    int64
}

func baseCheckEvaluation(check Check, expected string) CheckEvaluation {
	return CheckEvaluation{
		ID:       check.ID,
		Sequence: check.Sequence,
		Type:     check.Type,
		Expected: expected,
	}
}

func finishCheckEvaluation(evaluation CheckEvaluation, passed bool) CheckEvaluation {
	evaluation.Passed = passed
	if passed {
		evaluation.Status = CheckStatusPassed
	} else {
		evaluation.Status = CheckStatusFailed
	}
	return evaluation
}

func billLineItemCheckFilters(accountID string, check Check) (string, []any) {
	clauses := []string{"1 = 1"}
	args := []any{}
	if check.BillingPeriodStart != "" {
		clauses = append(clauses, "billing_period_start = ?", "billing_period_end = ?")
		args = append(args, check.BillingPeriodStart, check.BillingPeriodEnd)
	}
	if accountID != "" {
		clauses = append(clauses, "usage_account_id = ?")
		args = append(args, accountID)
	}
	for _, key := range sortedMapKeys(check.Tags) {
		clauses = append(clauses, `EXISTS (
			SELECT 1
			FROM json_each(bill_line_items.tag_snapshot_json) j
			WHERE j.key = ?
			  AND CAST(j.value AS TEXT) = ?
		)`)
		args = append(args, key, check.Tags[key])
	}
	return strings.Join(clauses, " AND "), args
}

func costCategoryRuleMatchesCheck(rule persistence.CostCategoryRule, check Check, accountID string) bool {
	if check.Value != "" && !strings.EqualFold(rule.Value, check.Value) {
		return false
	}
	if check.Service != "" && !costCategoryRuleHasValue(rule, persistence.CostCategoryRuleMatchService, "", check.Service) {
		return false
	}
	if accountID != "" && !costCategoryRuleHasValue(rule, persistence.CostCategoryRuleMatchAccount, "", accountID) {
		return false
	}
	for key, value := range check.Tags {
		if !costCategoryRuleHasValue(rule, persistence.CostCategoryRuleMatchTag, key, value) {
			return false
		}
	}
	return true
}

func costCategoryRuleHasValue(rule persistence.CostCategoryRule, dimension, tagKey, expected string) bool {
	for _, condition := range rule.Conditions {
		if condition.Dimension != dimension {
			continue
		}
		if tagKey != "" && condition.TagKey != tagKey {
			continue
		}
		for _, value := range condition.Values {
			if checkServiceDimensionValueMatches(dimension, expected, value) {
				return true
			}
		}
	}
	return false
}

func checkServiceDimensionValueMatches(dimension, expected, actual string) bool {
	if dimension == persistence.CostCategoryRuleMatchService {
		code := scenarioServiceCodeForName(expected)
		if code != "" && strings.EqualFold(code, actual) {
			return true
		}
	}
	return strings.EqualFold(expected, actual)
}

func costCategoryRuleExpectation(check Check) string {
	parts := []string{check.Category}
	if check.Value != "" {
		parts = append(parts, "value="+check.Value)
	}
	if check.Service != "" {
		parts = append(parts, "service="+check.Service)
	}
	if check.Account != "" || check.AccountID != "" {
		parts = append(parts, "account="+chooseFirst(check.Account, check.AccountID))
	}
	for _, key := range sortedMapKeys(check.Tags) {
		parts = append(parts, "tag:"+key+"="+check.Tags[key])
	}
	return strings.Join(parts, " ")
}

func billReconciliationExpectation(check Check, payerAccountID, expectedStatus string) string {
	return scopedStatusExpectation(check, payerAccountID, expectedStatus)
}

func paymentStatusExpectation(check Check, payerAccountID, expectedStatus string) string {
	return scopedStatusExpectation(check, payerAccountID, expectedStatus)
}

func scopedStatusExpectation(check Check, payerAccountID, expectedStatus string) string {
	parts := []string{"status=" + expectedStatus}
	if payerAccountID != "" {
		parts = append(parts, "payer="+payerAccountID)
	}
	if check.BillingPeriodStart != "" {
		parts = append(parts, "period="+check.BillingPeriodStart+".."+check.BillingPeriodEnd)
	}
	return strings.Join(parts, " ")
}

func checkPeriodMatches(check Check, periodStart, periodEnd string) bool {
	if check.BillingPeriodStart != "" && check.BillingPeriodStart != periodStart {
		return false
	}
	if check.BillingPeriodEnd != "" && check.BillingPeriodEnd != periodEnd {
		return false
	}
	return true
}

func checkServiceMatches(expected, actualCode, actualName string) bool {
	if strings.EqualFold(expected, actualCode) || strings.EqualFold(expected, actualName) {
		return true
	}
	expectedCode := scenarioServiceCodeForName(expected)
	if expectedCode != "" && strings.EqualFold(expectedCode, actualCode) {
		return true
	}
	if defaults, ok := scenarioServiceDefaultsByCode()[actualCode]; ok && strings.EqualFold(expected, defaults.ServiceName) {
		return true
	}
	return false
}

func normalizedCheckStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, " ", "_")
	return strings.ReplaceAll(value, "-", "_")
}

func statusMatches(expected, actual string) bool {
	expected = normalizedCheckStatus(expected)
	actual = normalizedCheckStatus(actual)
	if expected == actual {
		return true
	}
	return expected == "paid" && actual == "succeeded"
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
