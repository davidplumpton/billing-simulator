package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

const (
	costCategorySplitRuleStatusActive = "active"
	fixedSplitShareMicrosDenominator  = 1_000_000

	// CostCategorySplitMethodEven divides source value spend equally across targets.
	CostCategorySplitMethodEven = "even"
	// CostCategorySplitMethodFixed divides source value spend by target fixed-share percentages.
	CostCategorySplitMethodFixed = "fixed"
	// CostCategorySplitMethodProportional divides source value spend by each target value's direct spend.
	CostCategorySplitMethodProportional = "proportional"
)

// CostCategorySplitChargeRule stores one source-value allocation rule for a Cost Category.
type CostCategorySplitChargeRule struct {
	ID               string
	CostCategoryID   string
	CostCategoryName string
	SourceValue      string
	Method           string
	Description      string
	Status           string
	Targets          []CostCategorySplitChargeTarget
	CreatedAt        string
	UpdatedAt        string
}

// CostCategorySplitChargeTarget stores one ordered allocation target for a split rule.
type CostCategorySplitChargeTarget struct {
	ID               string
	RuleID           string
	TargetOrder      int
	TargetValue      string
	FixedShareMicros int
	CreatedAt        string
}

// CostCategorySplitChargeRuleCreateRequest describes a new split rule and its targets.
type CostCategorySplitChargeRuleCreateRequest struct {
	ID               string
	CostCategoryID   string
	CostCategoryName string
	SourceValue      string
	Method           string
	Description      string
	Status           string
	Targets          []CostCategorySplitChargeTargetCreateRequest
}

// CostCategorySplitChargeTargetCreateRequest describes one target value for a split rule.
type CostCategorySplitChargeTargetCreateRequest struct {
	ID               string
	TargetOrder      int
	TargetValue      string
	FixedShareMicros int
}

// CostCategorySplitChargeAllocation stores one audited allocation from a source line item to a target value.
type CostCategorySplitChargeAllocation struct {
	RuleID                   string
	SourceLineItemID         string
	CostCategoryID           string
	BillingPeriodStart       string
	BillingPeriodEnd         string
	PayerAccountID           string
	UsageAccountID           string
	SourceValue              string
	TargetValue              string
	Method                   string
	TargetOrder              int
	CurrencyCode             string
	SourceCostMicros         int64
	AllocationBaseCostMicros int64
	FixedShareMicros         int
	AllocatedCostMicros      int64
	CreatedAt                string
	UpdatedAt                string
}

// CostCategorySplitChargeAllocationListRequest filters persisted split-charge allocations.
type CostCategorySplitChargeAllocationListRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	RuleID             string
	CostCategoryID     string
	SourceLineItemID   string
	Limit              int
}

// CostCategorySplitChargeComparisonRequest selects one category and period for allocation reporting.
type CostCategorySplitChargeComparisonRequest struct {
	CostCategoryID     string
	BillingPeriodStart string
	BillingPeriodEnd   string
}

// CostCategorySplitChargeComparison reports direct and split-adjusted cost by Cost Category value.
type CostCategorySplitChargeComparison struct {
	CostCategoryID                string
	CostCategoryName              string
	BillingPeriodStart            string
	BillingPeriodEnd              string
	RawCostMicros                 int64
	CategoryCostMicros            int64
	SplitInCostMicros             int64
	SplitOutCostMicros            int64
	NetSplitCostMicros            int64
	TotalAllocatedCostMicros      int64
	UnallocatedResidualCostMicros int64
	Rows                          []CostCategorySplitChargeComparisonRow
}

// CostCategorySplitChargeComparisonRow shows one value's pre-split and post-split allocation totals.
type CostCategorySplitChargeComparisonRow struct {
	Value                         string
	PayerAccountID                string
	CurrencyCode                  string
	LineItemCount                 int
	SourceLineItemCount           int
	AllocationCount               int
	RawCostMicros                 int64
	CategoryCostMicros            int64
	SplitInCostMicros             int64
	SplitOutCostMicros            int64
	NetSplitCostMicros            int64
	TotalAllocatedCostMicros      int64
	UnallocatedResidualCostMicros int64
}

// CostCategorySplitChargeRefreshResult reports how many open-period allocation rows were rebuilt.
type CostCategorySplitChargeRefreshResult struct {
	BillingPeriodsRefreshed     int
	RulesEvaluated              int
	SourceLineItemsEvaluated    int
	AllocationsRefreshed        int
	SourceCostMicros            int64
	AllocatedCostMicros         int64
	UnallocatedSourceCostMicros int64
}

// CostCategorySplitChargeRepository manages split-charge rules and allocation snapshots.
type CostCategorySplitChargeRepository struct {
	db *sql.DB
}

// NewCostCategorySplitChargeRepository creates a repository backed by a workspace database.
func NewCostCategorySplitChargeRepository(db *sql.DB) CostCategorySplitChargeRepository {
	return CostCategorySplitChargeRepository{db: db}
}

// CreateRule saves one split-charge rule and refreshes open-period allocation audit rows.
func (r CostCategorySplitChargeRepository) CreateRule(ctx context.Context, request CostCategorySplitChargeRuleCreateRequest) (CostCategorySplitChargeRule, error) {
	if r.db == nil {
		return CostCategorySplitChargeRule{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategorySplitChargeRuleCreateRequest(request)
	if err := validateCostCategorySplitChargeRuleCreateRequest(request); err != nil {
		return CostCategorySplitChargeRule{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("ccs")
		if err != nil {
			return CostCategorySplitChargeRule{}, err
		}
		request.ID = id
	}
	for i := range request.Targets {
		if request.Targets[i].ID == "" {
			id, err := newRepositoryID("ccst")
			if err != nil {
				return CostCategorySplitChargeRule{}, err
			}
			request.Targets[i].ID = id
		}
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		categoryID, err := resolveCostCategoryID(ctx, tx, request.CostCategoryID, request.CostCategoryName)
		if err != nil {
			return err
		}
		request.CostCategoryID = categoryID
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO cost_category_split_charge_rules (
				id,
				cost_category_id,
				source_value,
				method,
				description,
				status
			) VALUES (?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.CostCategoryID,
			request.SourceValue,
			request.Method,
			request.Description,
			request.Status,
		); err != nil {
			return fmt.Errorf("insert cost category split charge rule %q: %w", request.ID, err)
		}
		for _, target := range request.Targets {
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO cost_category_split_charge_targets (
					id,
					rule_id,
					target_order,
					target_value,
					fixed_share_micros
				) VALUES (?, ?, ?, ?, ?)`,
				target.ID,
				request.ID,
				target.TargetOrder,
				target.TargetValue,
				target.FixedShareMicros,
			); err != nil {
				return fmt.Errorf("insert cost category split charge target %q: %w", target.TargetValue, err)
			}
		}
		_, err = refreshCostCategorySplitAllocationsInTx(ctx, tx, "", "")
		return err
	}); err != nil {
		return CostCategorySplitChargeRule{}, err
	}
	return r.GetRule(ctx, request.ID)
}

// GetRule loads one split-charge rule with ordered targets.
func (r CostCategorySplitChargeRepository) GetRule(ctx context.Context, id string) (CostCategorySplitChargeRule, error) {
	if r.db == nil {
		return CostCategorySplitChargeRule{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return CostCategorySplitChargeRule{}, fmt.Errorf("cost category split charge rule ID is required")
	}
	rule, err := scanCostCategorySplitChargeRule(r.db.QueryRowContext(
		ctx,
		`SELECT r.id,
		        r.cost_category_id,
		        c.name,
		        r.source_value,
		        r.method,
		        r.description,
		        r.status,
		        r.created_at,
		        r.updated_at
		 FROM cost_category_split_charge_rules r
		 JOIN cost_categories c ON c.id = r.cost_category_id
		 WHERE r.id = ?`,
		id,
	))
	if err != nil {
		return CostCategorySplitChargeRule{}, err
	}
	targets, err := listCostCategorySplitChargeTargets(ctx, r.db, rule.ID)
	if err != nil {
		return CostCategorySplitChargeRule{}, err
	}
	rule.Targets = targets
	return rule, nil
}

// ListRules returns split-charge rules for one Cost Category in source-value order.
func (r CostCategorySplitChargeRepository) ListRules(ctx context.Context, costCategoryID string) ([]CostCategorySplitChargeRule, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	costCategoryID = strings.TrimSpace(costCategoryID)
	if costCategoryID == "" {
		return nil, fmt.Errorf("cost category ID is required")
	}
	return listCostCategorySplitChargeRules(ctx, r.db, costCategoryID)
}

// RefreshAllocationsForOpenPeriods rebuilds split-charge rows for every non-finalized billing period.
func (r CostCategorySplitChargeRepository) RefreshAllocationsForOpenPeriods(ctx context.Context) (CostCategorySplitChargeRefreshResult, error) {
	if r.db == nil {
		return CostCategorySplitChargeRefreshResult{}, fmt.Errorf("database handle is required")
	}
	var result CostCategorySplitChargeRefreshResult
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		result, err = refreshCostCategorySplitAllocationsInTx(ctx, tx, "", "")
		return err
	})
	if err != nil {
		return CostCategorySplitChargeRefreshResult{}, err
	}
	return result, nil
}

// RefreshAllocationsForBillingPeriod rebuilds split-charge rows for one open billing period.
func (r CostCategorySplitChargeRepository) RefreshAllocationsForBillingPeriod(ctx context.Context, periodStart, periodEnd string) (CostCategorySplitChargeRefreshResult, error) {
	if r.db == nil {
		return CostCategorySplitChargeRefreshResult{}, fmt.Errorf("database handle is required")
	}
	periodStart = strings.TrimSpace(periodStart)
	periodEnd = strings.TrimSpace(periodEnd)
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return CostCategorySplitChargeRefreshResult{}, err
	}
	var result CostCategorySplitChargeRefreshResult
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		var err error
		result, err = refreshCostCategorySplitAllocationsInTx(ctx, tx, periodStart, periodEnd)
		return err
	})
	if err != nil {
		return CostCategorySplitChargeRefreshResult{}, err
	}
	return result, nil
}

// ListAllocations reads persisted split-charge allocation rows for reporting and tests.
func (r CostCategorySplitChargeRepository) ListAllocations(ctx context.Context, request CostCategorySplitChargeAllocationListRequest) ([]CostCategorySplitChargeAllocation, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategorySplitChargeAllocationListRequest(request)
	if err := validateCostCategorySplitChargeAllocationListRequest(request); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			rule_id,
			source_line_item_id,
			cost_category_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			source_value,
			target_value,
			method,
			target_order,
			currency_code,
			source_cost_micros,
			allocation_base_cost_micros,
			fixed_share_micros,
			allocated_cost_micros,
			created_at,
			updated_at
		 FROM cost_category_split_charge_allocations
		 WHERE (? = '' OR billing_period_start = ?)
		   AND (? = '' OR billing_period_end = ?)
		   AND (? = '' OR rule_id = ?)
		   AND (? = '' OR cost_category_id = ?)
		   AND (? = '' OR source_line_item_id = ?)
		 ORDER BY billing_period_start, billing_period_end, rule_id, source_line_item_id, target_order, target_value
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingPeriodEnd,
		request.RuleID,
		request.RuleID,
		request.CostCategoryID,
		request.CostCategoryID,
		request.SourceLineItemID,
		request.SourceLineItemID,
		request.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category split charge allocations: %w", err)
	}
	defer rows.Close()

	var allocations []CostCategorySplitChargeAllocation
	for rows.Next() {
		allocation, err := scanCostCategorySplitChargeAllocation(rows)
		if err != nil {
			return nil, err
		}
		allocations = append(allocations, allocation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category split charge allocations: %w", err)
	}
	return allocations, nil
}

// CompareAllocations summarizes raw category costs, split movement, and residuals for one billing period.
func (r CostCategorySplitChargeRepository) CompareAllocations(ctx context.Context, request CostCategorySplitChargeComparisonRequest) (CostCategorySplitChargeComparison, error) {
	if r.db == nil {
		return CostCategorySplitChargeComparison{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostCategorySplitChargeComparisonRequest(request)
	if err := validateCostCategorySplitChargeComparisonRequest(request); err != nil {
		return CostCategorySplitChargeComparison{}, err
	}

	comparison := CostCategorySplitChargeComparison{
		CostCategoryID:     request.CostCategoryID,
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
	}
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT name
		 FROM cost_categories
		 WHERE id = ?`,
		request.CostCategoryID,
	).Scan(&comparison.CostCategoryName); err != nil {
		if err == sql.ErrNoRows {
			return CostCategorySplitChargeComparison{}, fmt.Errorf("cost category %q not found", request.CostCategoryID)
		}
		return CostCategorySplitChargeComparison{}, fmt.Errorf("read cost category for split comparison: %w", err)
	}

	rowsByKey := map[costCategorySplitComparisonKey]*CostCategorySplitChargeComparisonRow{}
	if err := r.addRawCostComparisonRows(ctx, request, rowsByKey); err != nil {
		return CostCategorySplitChargeComparison{}, err
	}
	if err := r.addSplitInComparisonRows(ctx, request, rowsByKey); err != nil {
		return CostCategorySplitChargeComparison{}, err
	}
	if err := r.addSplitOutComparisonRows(ctx, request, rowsByKey); err != nil {
		return CostCategorySplitChargeComparison{}, err
	}

	keys := make([]costCategorySplitComparisonKey, 0, len(rowsByKey))
	for key := range rowsByKey {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left := strings.ToLower(keys[i].value)
		right := strings.ToLower(keys[j].value)
		if left != right {
			return left < right
		}
		if keys[i].payerAccountID != keys[j].payerAccountID {
			return keys[i].payerAccountID < keys[j].payerAccountID
		}
		return keys[i].currencyCode < keys[j].currencyCode
	})

	for _, key := range keys {
		row := *rowsByKey[key]
		row.CategoryCostMicros = row.RawCostMicros - row.SplitOutCostMicros
		row.NetSplitCostMicros = row.SplitInCostMicros - row.SplitOutCostMicros
		row.TotalAllocatedCostMicros = row.CategoryCostMicros + row.SplitInCostMicros
		comparison.RawCostMicros += row.RawCostMicros
		comparison.CategoryCostMicros += row.CategoryCostMicros
		comparison.SplitInCostMicros += row.SplitInCostMicros
		comparison.SplitOutCostMicros += row.SplitOutCostMicros
		comparison.NetSplitCostMicros += row.NetSplitCostMicros
		comparison.TotalAllocatedCostMicros += row.TotalAllocatedCostMicros
		comparison.UnallocatedResidualCostMicros += row.UnallocatedResidualCostMicros
		comparison.Rows = append(comparison.Rows, row)
	}
	return comparison, nil
}

func (r CostCategorySplitChargeRepository) addRawCostComparisonRows(ctx context.Context, request CostCategorySplitChargeComparisonRequest, rowsByKey map[costCategorySplitComparisonKey]*CostCategorySplitChargeComparisonRow) error {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			assigned_value,
			payer_account_id,
			currency_code,
			COUNT(*),
			COALESCE(SUM(unblended_cost_micros), 0)
		 FROM cost_category_line_item_assignments
		 WHERE cost_category_id = ?
		   AND billing_period_start = ?
		   AND billing_period_end = ?
		 GROUP BY assigned_value, payer_account_id, currency_code
		 ORDER BY lower(assigned_value), payer_account_id, currency_code`,
		request.CostCategoryID,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
	)
	if err != nil {
		return fmt.Errorf("list raw split comparison costs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var value, payerAccountID, currencyCode string
		var lineItems int
		var rawCostMicros int64
		if err := rows.Scan(&value, &payerAccountID, &currencyCode, &lineItems, &rawCostMicros); err != nil {
			return fmt.Errorf("scan raw split comparison cost: %w", err)
		}
		row := splitComparisonRow(rowsByKey, costCategorySplitComparisonKey{
			value:          value,
			payerAccountID: payerAccountID,
			currencyCode:   currencyCode,
		})
		row.LineItemCount += lineItems
		row.RawCostMicros += rawCostMicros
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate raw split comparison costs: %w", err)
	}
	return nil
}

func (r CostCategorySplitChargeRepository) addSplitInComparisonRows(ctx context.Context, request CostCategorySplitChargeComparisonRequest, rowsByKey map[costCategorySplitComparisonKey]*CostCategorySplitChargeComparisonRow) error {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			target_value,
			payer_account_id,
			currency_code,
			COUNT(*),
			COALESCE(SUM(allocated_cost_micros), 0)
		 FROM cost_category_split_charge_allocations
		 WHERE cost_category_id = ?
		   AND billing_period_start = ?
		   AND billing_period_end = ?
		 GROUP BY target_value, payer_account_id, currency_code
		 ORDER BY lower(target_value), payer_account_id, currency_code`,
		request.CostCategoryID,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
	)
	if err != nil {
		return fmt.Errorf("list split-in comparison costs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var value, payerAccountID, currencyCode string
		var allocations int
		var splitInCostMicros int64
		if err := rows.Scan(&value, &payerAccountID, &currencyCode, &allocations, &splitInCostMicros); err != nil {
			return fmt.Errorf("scan split-in comparison cost: %w", err)
		}
		row := splitComparisonRow(rowsByKey, costCategorySplitComparisonKey{
			value:          value,
			payerAccountID: payerAccountID,
			currencyCode:   currencyCode,
		})
		row.AllocationCount += allocations
		row.SplitInCostMicros += splitInCostMicros
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate split-in comparison costs: %w", err)
	}
	return nil
}

func (r CostCategorySplitChargeRepository) addSplitOutComparisonRows(ctx context.Context, request CostCategorySplitChargeComparisonRequest, rowsByKey map[costCategorySplitComparisonKey]*CostCategorySplitChargeComparisonRow) error {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			source_value,
			payer_account_id,
			currency_code,
			COUNT(*),
			COALESCE(SUM(source_cost_micros), 0),
			COALESCE(SUM(split_out_cost_micros), 0),
			COALESCE(SUM(source_cost_micros - split_out_cost_micros), 0)
		 FROM (
			SELECT
				rule_id,
				source_line_item_id,
				source_value,
				payer_account_id,
				currency_code,
				MAX(source_cost_micros) AS source_cost_micros,
				SUM(allocated_cost_micros) AS split_out_cost_micros
			FROM cost_category_split_charge_allocations
			WHERE cost_category_id = ?
			  AND billing_period_start = ?
			  AND billing_period_end = ?
			GROUP BY rule_id, source_line_item_id, source_value, payer_account_id, currency_code
		 ) source_allocations
		 GROUP BY source_value, payer_account_id, currency_code
		 ORDER BY lower(source_value), payer_account_id, currency_code`,
		request.CostCategoryID,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
	)
	if err != nil {
		return fmt.Errorf("list split-out comparison costs: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var value, payerAccountID, currencyCode string
		var sourceLineItems int
		var sourceCostMicros, splitOutCostMicros, residualCostMicros int64
		if err := rows.Scan(&value, &payerAccountID, &currencyCode, &sourceLineItems, &sourceCostMicros, &splitOutCostMicros, &residualCostMicros); err != nil {
			return fmt.Errorf("scan split-out comparison cost: %w", err)
		}
		row := splitComparisonRow(rowsByKey, costCategorySplitComparisonKey{
			value:          value,
			payerAccountID: payerAccountID,
			currencyCode:   currencyCode,
		})
		row.SourceLineItemCount += sourceLineItems
		row.SplitOutCostMicros += splitOutCostMicros
		row.UnallocatedResidualCostMicros += residualCostMicros
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate split-out comparison costs: %w", err)
	}
	return nil
}

type costCategorySplitComparisonKey struct {
	value          string
	payerAccountID string
	currencyCode   string
}

func splitComparisonRow(rowsByKey map[costCategorySplitComparisonKey]*CostCategorySplitChargeComparisonRow, key costCategorySplitComparisonKey) *CostCategorySplitChargeComparisonRow {
	row, ok := rowsByKey[key]
	if !ok {
		row = &CostCategorySplitChargeComparisonRow{
			Value:          key.value,
			PayerAccountID: key.payerAccountID,
			CurrencyCode:   key.currencyCode,
		}
		rowsByKey[key] = row
	}
	return row
}

func refreshCostCategorySplitAllocationsInTx(ctx context.Context, tx costCategoryAssignmentStore, periodStart, periodEnd string) (CostCategorySplitChargeRefreshResult, error) {
	rules, err := listCostCategorySplitChargeRules(ctx, tx, "")
	if err != nil {
		return CostCategorySplitChargeRefreshResult{}, err
	}
	result := CostCategorySplitChargeRefreshResult{
		RulesEvaluated: len(rules),
	}
	if len(rules) == 0 {
		return result, nil
	}
	if err := clearOpenCostCategorySplitAllocations(ctx, tx, periodStart, periodEnd); err != nil {
		return CostCategorySplitChargeRefreshResult{}, err
	}

	periods := map[string]bool{}
	baseCache := map[string]map[string]int64{}
	for _, rule := range rules {
		if rule.Status != costCategorySplitRuleStatusActive {
			continue
		}
		sources, err := listCostCategorySplitSourceAssignments(ctx, tx, rule, periodStart, periodEnd)
		if err != nil {
			return CostCategorySplitChargeRefreshResult{}, err
		}
		for _, source := range sources {
			periods[source.BillingPeriodStart+"|"+source.BillingPeriodEnd] = true
			result.SourceLineItemsEvaluated++
			result.SourceCostMicros += source.UnblendedCostMicros

			cacheKey := strings.Join([]string{
				rule.ID,
				source.BillingPeriodStart,
				source.BillingPeriodEnd,
				source.PayerAccountID,
				source.CurrencyCode,
			}, "|")
			targetBases, ok := baseCache[cacheKey]
			if !ok {
				targetBases, err = listCostCategorySplitTargetBases(ctx, tx, rule, source)
				if err != nil {
					return CostCategorySplitChargeRefreshResult{}, err
				}
				baseCache[cacheKey] = targetBases
			}
			weights := costCategorySplitWeights(rule, targetBases)
			amounts := allocateMicrosByWeights(source.UnblendedCostMicros, weights, rule.Targets)
			for i, target := range rule.Targets {
				allocation := CostCategorySplitChargeAllocation{
					RuleID:                   rule.ID,
					SourceLineItemID:         source.LineItemID,
					CostCategoryID:           rule.CostCategoryID,
					BillingPeriodStart:       source.BillingPeriodStart,
					BillingPeriodEnd:         source.BillingPeriodEnd,
					PayerAccountID:           source.PayerAccountID,
					UsageAccountID:           source.UsageAccountID,
					SourceValue:              rule.SourceValue,
					TargetValue:              target.TargetValue,
					Method:                   rule.Method,
					TargetOrder:              target.TargetOrder,
					CurrencyCode:             source.CurrencyCode,
					SourceCostMicros:         source.UnblendedCostMicros,
					AllocationBaseCostMicros: targetBases[target.TargetValue],
					FixedShareMicros:         target.FixedShareMicros,
					AllocatedCostMicros:      amounts[i],
				}
				if err := insertCostCategorySplitChargeAllocation(ctx, tx, allocation); err != nil {
					return CostCategorySplitChargeRefreshResult{}, err
				}
				result.AllocationsRefreshed++
				result.AllocatedCostMicros += allocation.AllocatedCostMicros
			}
		}
	}
	result.BillingPeriodsRefreshed = len(periods)
	result.UnallocatedSourceCostMicros = result.SourceCostMicros - result.AllocatedCostMicros
	return result, nil
}

func clearOpenCostCategorySplitAllocations(ctx context.Context, tx costCategoryAssignmentStore, periodStart, periodEnd string) error {
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM cost_category_split_charge_allocations
		 WHERE (? = '' OR billing_period_start = ?)
		   AND (? = '' OR billing_period_end = ?)
		   AND NOT EXISTS (
			SELECT 1
			FROM billing_period_closes c
			WHERE c.billing_period_start = cost_category_split_charge_allocations.billing_period_start
			  AND c.billing_period_end = cost_category_split_charge_allocations.billing_period_end
			  AND c.payer_account_id = cost_category_split_charge_allocations.payer_account_id
			  AND c.status = ?
		   )`,
		periodStart,
		periodStart,
		periodEnd,
		periodEnd,
		billingPeriodCloseStatusClosed,
	); err != nil {
		return fmt.Errorf("clear cost category split allocations: %w", err)
	}
	return nil
}

func listCostCategorySplitSourceAssignments(ctx context.Context, q costCategoryQueryer, rule CostCategorySplitChargeRule, periodStart, periodEnd string) ([]CostCategoryLineItemAssignment, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT
			a.line_item_id,
			a.cost_category_id,
			a.billing_period_start,
			a.billing_period_end,
			a.payer_account_id,
			a.usage_account_id,
			a.line_item_status,
			a.cost_category_name,
			a.category_default_value,
			a.assigned_value,
			a.assignment_source,
			a.matched_rule_id,
			a.matched_rule_order,
			a.matched_rule_value,
			a.currency_code,
			a.unblended_cost_micros,
			a.created_at,
			a.updated_at
		 FROM cost_category_line_item_assignments a
		 WHERE a.cost_category_id = ?
		   AND a.assigned_value = ?
		   AND (? = '' OR a.billing_period_start = ?)
		   AND (? = '' OR a.billing_period_end = ?)
		   AND NOT EXISTS (
			SELECT 1
			FROM billing_period_closes c
			WHERE c.billing_period_start = a.billing_period_start
			  AND c.billing_period_end = a.billing_period_end
			  AND c.payer_account_id = a.payer_account_id
			  AND c.status = ?
		   )
		 ORDER BY a.billing_period_start, a.billing_period_end, a.payer_account_id, a.currency_code, a.line_item_id`,
		rule.CostCategoryID,
		rule.SourceValue,
		periodStart,
		periodStart,
		periodEnd,
		periodEnd,
		billingPeriodCloseStatusClosed,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category split source assignments: %w", err)
	}
	defer rows.Close()

	var assignments []CostCategoryLineItemAssignment
	for rows.Next() {
		assignment, err := scanCostCategoryLineItemAssignment(rows)
		if err != nil {
			return nil, err
		}
		assignments = append(assignments, assignment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category split source assignments: %w", err)
	}
	return assignments, nil
}

func listCostCategorySplitTargetBases(ctx context.Context, q costCategoryQueryer, rule CostCategorySplitChargeRule, source CostCategoryLineItemAssignment) (map[string]int64, error) {
	targetValues := map[string]bool{}
	bases := map[string]int64{}
	for _, target := range rule.Targets {
		targetValues[target.TargetValue] = true
		bases[target.TargetValue] = 0
	}
	rows, err := q.QueryContext(
		ctx,
		`SELECT assigned_value, COALESCE(SUM(unblended_cost_micros), 0)
		 FROM cost_category_line_item_assignments
		 WHERE cost_category_id = ?
		   AND billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND currency_code = ?
		   AND assigned_value <> ?
		   AND NOT EXISTS (
			SELECT 1
			FROM billing_period_closes c
			WHERE c.billing_period_start = cost_category_line_item_assignments.billing_period_start
			  AND c.billing_period_end = cost_category_line_item_assignments.billing_period_end
			  AND c.payer_account_id = cost_category_line_item_assignments.payer_account_id
			  AND c.status = ?
		   )
		 GROUP BY assigned_value
		 ORDER BY assigned_value`,
		rule.CostCategoryID,
		source.BillingPeriodStart,
		source.BillingPeriodEnd,
		source.PayerAccountID,
		source.CurrencyCode,
		rule.SourceValue,
		billingPeriodCloseStatusClosed,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category split target bases: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		var costMicros int64
		if err := rows.Scan(&value, &costMicros); err != nil {
			return nil, fmt.Errorf("scan cost category split target base: %w", err)
		}
		if targetValues[value] {
			bases[value] = costMicros
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category split target bases: %w", err)
	}
	return bases, nil
}

func costCategorySplitWeights(rule CostCategorySplitChargeRule, targetBases map[string]int64) []int64 {
	weights := make([]int64, len(rule.Targets))
	for i, target := range rule.Targets {
		switch rule.Method {
		case CostCategorySplitMethodFixed:
			weights[i] = int64(target.FixedShareMicros)
		case CostCategorySplitMethodProportional:
			weights[i] = targetBases[target.TargetValue]
		default:
			weights[i] = 1
		}
	}
	return weights
}

func allocateMicrosByWeights(totalMicros int64, weights []int64, targets []CostCategorySplitChargeTarget) []int64 {
	allocations := make([]int64, len(weights))
	if totalMicros <= 0 || len(weights) == 0 {
		return allocations
	}
	var totalWeight int64
	for _, weight := range weights {
		if weight > 0 {
			totalWeight += weight
		}
	}
	if totalWeight <= 0 {
		return allocations
	}

	type remainder struct {
		index       int
		value       int64
		targetOrder int
		targetValue string
	}
	var remainders []remainder
	var allocated int64
	for i, weight := range weights {
		if weight <= 0 {
			continue
		}
		share, weightedRemainder := splitWeightedShare(totalMicros, weight, totalWeight)
		allocations[i] = share
		allocated += share
		remainders = append(remainders, remainder{
			index:       i,
			value:       weightedRemainder,
			targetOrder: targets[i].TargetOrder,
			targetValue: targets[i].TargetValue,
		})
	}
	sort.Slice(remainders, func(i, j int) bool {
		if remainders[i].value == remainders[j].value {
			if remainders[i].targetOrder == remainders[j].targetOrder {
				return remainders[i].targetValue < remainders[j].targetValue
			}
			return remainders[i].targetOrder < remainders[j].targetOrder
		}
		return remainders[i].value > remainders[j].value
	})
	residual := totalMicros - allocated
	for _, rem := range remainders {
		if residual <= 0 {
			break
		}
		if rem.value <= 0 {
			continue
		}
		allocations[rem.index]++
		residual--
	}
	return allocations
}

func splitWeightedShare(totalMicros, weight, totalWeight int64) (int64, int64) {
	var product big.Int
	product.Mul(big.NewInt(totalMicros), big.NewInt(weight))
	var quotient big.Int
	var remainder big.Int
	quotient.QuoRem(&product, big.NewInt(totalWeight), &remainder)
	return quotient.Int64(), remainder.Int64()
}

func insertCostCategorySplitChargeAllocation(ctx context.Context, tx costCategoryAssignmentStore, allocation CostCategorySplitChargeAllocation) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO cost_category_split_charge_allocations (
			rule_id,
			source_line_item_id,
			cost_category_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			source_value,
			target_value,
			method,
			target_order,
			currency_code,
			source_cost_micros,
			allocation_base_cost_micros,
			fixed_share_micros,
			allocated_cost_micros
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		allocation.RuleID,
		allocation.SourceLineItemID,
		allocation.CostCategoryID,
		allocation.BillingPeriodStart,
		allocation.BillingPeriodEnd,
		allocation.PayerAccountID,
		allocation.UsageAccountID,
		allocation.SourceValue,
		allocation.TargetValue,
		allocation.Method,
		allocation.TargetOrder,
		allocation.CurrencyCode,
		allocation.SourceCostMicros,
		allocation.AllocationBaseCostMicros,
		allocation.FixedShareMicros,
		allocation.AllocatedCostMicros,
	); err != nil {
		return fmt.Errorf("insert cost category split allocation for source line item %q target %q: %w", allocation.SourceLineItemID, allocation.TargetValue, err)
	}
	return nil
}

func listCostCategorySplitChargeRules(ctx context.Context, q costCategoryQueryer, costCategoryID string) ([]CostCategorySplitChargeRule, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT r.id,
		        r.cost_category_id,
		        c.name,
		        r.source_value,
		        r.method,
		        r.description,
		        r.status,
		        r.created_at,
		        r.updated_at
		 FROM cost_category_split_charge_rules r
		 JOIN cost_categories c ON c.id = r.cost_category_id
		 WHERE (? = '' OR r.cost_category_id = ?)
		 ORDER BY lower(c.name), r.source_value, r.id`,
		costCategoryID,
		costCategoryID,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category split charge rules: %w", err)
	}
	defer rows.Close()

	var rules []CostCategorySplitChargeRule
	for rows.Next() {
		rule, err := scanCostCategorySplitChargeRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category split charge rules: %w", err)
	}
	for i := range rules {
		targets, err := listCostCategorySplitChargeTargets(ctx, q, rules[i].ID)
		if err != nil {
			return nil, err
		}
		rules[i].Targets = targets
	}
	return rules, nil
}

func listCostCategorySplitChargeTargets(ctx context.Context, q costCategoryQueryer, ruleID string) ([]CostCategorySplitChargeTarget, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT id,
		        rule_id,
		        target_order,
		        target_value,
		        fixed_share_micros,
		        created_at
		 FROM cost_category_split_charge_targets
		 WHERE rule_id = ?
		 ORDER BY target_order, target_value`,
		ruleID,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost category split charge targets: %w", err)
	}
	defer rows.Close()

	var targets []CostCategorySplitChargeTarget
	for rows.Next() {
		target, err := scanCostCategorySplitChargeTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost category split charge targets: %w", err)
	}
	return targets, nil
}

func scanCostCategorySplitChargeRule(row costCategoryRow) (CostCategorySplitChargeRule, error) {
	var rule CostCategorySplitChargeRule
	if err := row.Scan(
		&rule.ID,
		&rule.CostCategoryID,
		&rule.CostCategoryName,
		&rule.SourceValue,
		&rule.Method,
		&rule.Description,
		&rule.Status,
		&rule.CreatedAt,
		&rule.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return CostCategorySplitChargeRule{}, fmt.Errorf("cost category split charge rule not found")
		}
		return CostCategorySplitChargeRule{}, fmt.Errorf("scan cost category split charge rule: %w", err)
	}
	return rule, nil
}

func scanCostCategorySplitChargeTarget(row costCategoryRow) (CostCategorySplitChargeTarget, error) {
	var target CostCategorySplitChargeTarget
	if err := row.Scan(
		&target.ID,
		&target.RuleID,
		&target.TargetOrder,
		&target.TargetValue,
		&target.FixedShareMicros,
		&target.CreatedAt,
	); err != nil {
		return CostCategorySplitChargeTarget{}, fmt.Errorf("scan cost category split charge target: %w", err)
	}
	return target, nil
}

func scanCostCategorySplitChargeAllocation(row costCategoryRow) (CostCategorySplitChargeAllocation, error) {
	var allocation CostCategorySplitChargeAllocation
	if err := row.Scan(
		&allocation.RuleID,
		&allocation.SourceLineItemID,
		&allocation.CostCategoryID,
		&allocation.BillingPeriodStart,
		&allocation.BillingPeriodEnd,
		&allocation.PayerAccountID,
		&allocation.UsageAccountID,
		&allocation.SourceValue,
		&allocation.TargetValue,
		&allocation.Method,
		&allocation.TargetOrder,
		&allocation.CurrencyCode,
		&allocation.SourceCostMicros,
		&allocation.AllocationBaseCostMicros,
		&allocation.FixedShareMicros,
		&allocation.AllocatedCostMicros,
		&allocation.CreatedAt,
		&allocation.UpdatedAt,
	); err != nil {
		return CostCategorySplitChargeAllocation{}, fmt.Errorf("scan cost category split charge allocation: %w", err)
	}
	return allocation, nil
}

func normalizeCostCategorySplitChargeRuleCreateRequest(request CostCategorySplitChargeRuleCreateRequest) CostCategorySplitChargeRuleCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.CostCategoryID = strings.TrimSpace(request.CostCategoryID)
	request.CostCategoryName = strings.TrimSpace(request.CostCategoryName)
	request.SourceValue = strings.TrimSpace(request.SourceValue)
	request.Method = strings.TrimSpace(request.Method)
	request.Description = strings.TrimSpace(request.Description)
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = costCategorySplitRuleStatusActive
	}
	for i := range request.Targets {
		target := request.Targets[i]
		target.ID = strings.TrimSpace(target.ID)
		target.TargetValue = strings.TrimSpace(target.TargetValue)
		if target.TargetOrder == 0 {
			target.TargetOrder = i + 1
		}
		request.Targets[i] = target
	}
	sort.Slice(request.Targets, func(i, j int) bool {
		if request.Targets[i].TargetOrder == request.Targets[j].TargetOrder {
			return request.Targets[i].TargetValue < request.Targets[j].TargetValue
		}
		return request.Targets[i].TargetOrder < request.Targets[j].TargetOrder
	})
	return request
}

func validateCostCategorySplitChargeRuleCreateRequest(request CostCategorySplitChargeRuleCreateRequest) error {
	if request.CostCategoryID == "" && request.CostCategoryName == "" {
		return fmt.Errorf("cost category ID or name is required")
	}
	if request.SourceValue == "" {
		return fmt.Errorf("cost category split source value is required")
	}
	switch request.Method {
	case CostCategorySplitMethodEven, CostCategorySplitMethodFixed, CostCategorySplitMethodProportional:
	default:
		return fmt.Errorf("cost category split method %q is not supported", request.Method)
	}
	switch request.Status {
	case costCategorySplitRuleStatusActive, "archived":
	default:
		return fmt.Errorf("cost category split status %q is not supported", request.Status)
	}
	if len(request.Targets) < 2 {
		return fmt.Errorf("cost category split rule needs at least two target values")
	}
	orders := map[int]bool{}
	values := map[string]bool{}
	var fixedShareSum int
	for i, target := range request.Targets {
		if target.TargetOrder <= 0 {
			return fmt.Errorf("cost category split target %d order must be positive", i)
		}
		if orders[target.TargetOrder] {
			return fmt.Errorf("cost category split target order %d is duplicated", target.TargetOrder)
		}
		orders[target.TargetOrder] = true
		if target.TargetValue == "" {
			return fmt.Errorf("cost category split target %d value is required", i)
		}
		if target.TargetValue == request.SourceValue {
			return fmt.Errorf("cost category split target %q cannot also be the source value", target.TargetValue)
		}
		if values[target.TargetValue] {
			return fmt.Errorf("cost category split target value %q is duplicated", target.TargetValue)
		}
		values[target.TargetValue] = true
		if target.FixedShareMicros < 0 {
			return fmt.Errorf("cost category split target %q fixed share cannot be negative", target.TargetValue)
		}
		if request.Method == CostCategorySplitMethodFixed {
			if target.FixedShareMicros <= 0 {
				return fmt.Errorf("fixed split target %q needs a positive fixed share", target.TargetValue)
			}
			fixedShareSum += target.FixedShareMicros
		} else if target.FixedShareMicros != 0 {
			return fmt.Errorf("%s split target %q cannot set a fixed share", request.Method, target.TargetValue)
		}
	}
	if request.Method == CostCategorySplitMethodFixed && fixedShareSum != fixedSplitShareMicrosDenominator {
		return fmt.Errorf("fixed split shares sum to %d micros, want %d", fixedShareSum, fixedSplitShareMicrosDenominator)
	}
	return nil
}

func normalizeCostCategorySplitChargeAllocationListRequest(request CostCategorySplitChargeAllocationListRequest) CostCategorySplitChargeAllocationListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.RuleID = strings.TrimSpace(request.RuleID)
	request.CostCategoryID = strings.TrimSpace(request.CostCategoryID)
	request.SourceLineItemID = strings.TrimSpace(request.SourceLineItemID)
	if request.Limit <= 0 {
		request.Limit = defaultCostCategoryAssignmentLimit
	}
	if request.Limit > maxCostCategoryAssignmentLimit {
		request.Limit = maxCostCategoryAssignmentLimit
	}
	return request
}

func validateCostCategorySplitChargeAllocationListRequest(request CostCategorySplitChargeAllocationListRequest) error {
	if request.BillingPeriodStart == "" && request.BillingPeriodEnd == "" {
		return nil
	}
	if request.BillingPeriodStart == "" || request.BillingPeriodEnd == "" {
		return fmt.Errorf("billing period start and end are both required when filtering split allocations by period")
	}
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}

func normalizeCostCategorySplitChargeComparisonRequest(request CostCategorySplitChargeComparisonRequest) CostCategorySplitChargeComparisonRequest {
	request.CostCategoryID = strings.TrimSpace(request.CostCategoryID)
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}

func validateCostCategorySplitChargeComparisonRequest(request CostCategorySplitChargeComparisonRequest) error {
	if request.CostCategoryID == "" {
		return fmt.Errorf("cost category ID is required")
	}
	return validateBillingPeriodDateRange(request.BillingPeriodStart, request.BillingPeriodEnd)
}
