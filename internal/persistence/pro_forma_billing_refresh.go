package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
)

// RefreshLineItems rebuilds internal showback rows from current bill line items.
func (r ProFormaBillingRepository) RefreshLineItems(ctx context.Context, request ProFormaRefreshRequest) (ProFormaRefreshResult, error) {
	if r.db == nil {
		return ProFormaRefreshResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeProFormaRefreshRequest(request)
	if err := validateProFormaRefreshRequest(request); err != nil {
		return ProFormaRefreshResult{}, err
	}
	var result ProFormaRefreshResult
	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		sources, err := listProFormaSourceLineItems(ctx, tx, request)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM pro_forma_line_items
			 WHERE billing_period_start = ?
			   AND billing_period_end = ?
			   AND (? = '' OR billing_group_id = ?)
			   AND (? = '' OR payer_account_id = ?)`,
			request.BillingPeriodStart,
			request.BillingPeriodEnd,
			request.BillingGroupID,
			request.BillingGroupID,
			request.PayerAccountID,
			request.PayerAccountID,
		); err != nil {
			return fmt.Errorf("clear pro forma line items: %w", err)
		}
		seenGroups := map[string]bool{}
		for _, source := range sources {
			proFormaRate, err := multiplyMicrosByBasisPoints(source.SourceRateMicros, source.RateMultiplierBasisPoints)
			if err != nil {
				return fmt.Errorf("calculate pro forma rate for %s: %w", source.SourceBillLineItemID, err)
			}
			proFormaCost, err := multiplyMicrosByBasisPoints(source.SourceCostMicros, source.RateMultiplierBasisPoints)
			if err != nil {
				return fmt.Errorf("calculate pro forma cost for %s: %w", source.SourceBillLineItemID, err)
			}
			id, err := newRepositoryID("pfli")
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO pro_forma_line_items (
					id,
					source_bill_line_item_id,
					billing_group_id,
					pricing_plan_id,
					pricing_rule_id,
					billing_period_start,
					billing_period_end,
					payer_account_id,
					usage_account_id,
					service_code,
					service_name,
					usage_type,
					line_item_status,
					currency_code,
					source_rate_micros,
					source_cost_micros,
					rate_multiplier_basis_points,
					pro_forma_rate_micros,
					pro_forma_cost_micros,
					adjustment_micros
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id,
				source.SourceBillLineItemID,
				source.BillingGroupID,
				source.PricingPlanID,
				nullStringArg(source.PricingRuleID),
				source.BillingPeriodStart,
				source.BillingPeriodEnd,
				source.PayerAccountID,
				source.UsageAccountID,
				source.ServiceCode,
				source.ServiceName,
				source.UsageType,
				source.LineItemStatus,
				source.CurrencyCode,
				source.SourceRateMicros,
				source.SourceCostMicros,
				source.RateMultiplierBasisPoints,
				proFormaRate,
				proFormaCost,
				proFormaCost-source.SourceCostMicros,
			); err != nil {
				return fmt.Errorf("insert pro forma line item for %s: %w", source.SourceBillLineItemID, err)
			}
			seenGroups[source.BillingGroupID] = true
			result.SourceLineItems++
			result.ProFormaLineItems++
			result.SourceCostMicros += source.SourceCostMicros
			result.ProFormaCostMicros += proFormaCost
			result.AdjustmentMicros += proFormaCost - source.SourceCostMicros
		}
		result.BillingGroupsRefreshed = len(seenGroups)
		return nil
	}); err != nil {
		return ProFormaRefreshResult{}, err
	}
	return result, nil
}

type proFormaSourceLineItem struct {
	SourceBillLineItemID      string
	BillingGroupID            string
	PricingPlanID             string
	PricingRuleID             string
	BillingPeriodStart        string
	BillingPeriodEnd          string
	PayerAccountID            string
	UsageAccountID            string
	ServiceCode               string
	ServiceName               string
	UsageType                 string
	LineItemStatus            string
	CurrencyCode              string
	SourceRateMicros          int64
	SourceCostMicros          int64
	RateMultiplierBasisPoints int
}

func listProFormaSourceLineItems(ctx context.Context, tx *sql.Tx, request ProFormaRefreshRequest) ([]proFormaSourceLineItem, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT
			li.id,
			g.id,
			p.id,
			COALESCE(r.id, ''),
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.usage_type,
			li.line_item_status,
			li.currency_code,
			li.unblended_rate_micros,
			li.unblended_cost_micros,
			COALESCE(r.rate_multiplier_basis_points, ?)
		 FROM bill_line_items li
		 JOIN pro_forma_billing_group_accounts a ON a.account_id = li.usage_account_id
		 JOIN pro_forma_billing_groups g ON g.id = a.billing_group_id
		 JOIN pro_forma_pricing_plans p ON p.id = g.pricing_plan_id
		 LEFT JOIN pro_forma_pricing_rules r ON r.pricing_plan_id = p.id
		   AND r.service_code = li.service_code
		   AND r.status = ?
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = g.payer_account_id
		   AND g.status = ?
		   AND p.status = ?
		   AND (? = '' OR g.id = ?)
		   AND (? = '' OR li.payer_account_id = ?)
		 ORDER BY g.name, li.id`,
		proFormaDefaultMultiplierBPS,
		proFormaStatusActive,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		proFormaStatusActive,
		proFormaStatusActive,
		request.BillingGroupID,
		request.BillingGroupID,
		request.PayerAccountID,
		request.PayerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list pro forma source line items: %w", err)
	}
	defer rows.Close()
	var sources []proFormaSourceLineItem
	for rows.Next() {
		var source proFormaSourceLineItem
		if err := rows.Scan(
			&source.SourceBillLineItemID,
			&source.BillingGroupID,
			&source.PricingPlanID,
			&source.PricingRuleID,
			&source.BillingPeriodStart,
			&source.BillingPeriodEnd,
			&source.PayerAccountID,
			&source.UsageAccountID,
			&source.ServiceCode,
			&source.ServiceName,
			&source.UsageType,
			&source.LineItemStatus,
			&source.CurrencyCode,
			&source.SourceRateMicros,
			&source.SourceCostMicros,
			&source.RateMultiplierBasisPoints,
		); err != nil {
			return nil, fmt.Errorf("scan pro forma source line item: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pro forma source line items: %w", err)
	}
	return sources, nil
}

func multiplyMicrosByBasisPoints(value int64, basisPoints int) (int64, error) {
	if value < 0 {
		return 0, fmt.Errorf("micros value cannot be negative")
	}
	if basisPoints <= 0 {
		return 0, fmt.Errorf("basis points must be positive")
	}
	numerator := big.NewInt(value)
	numerator.Mul(numerator, big.NewInt(int64(basisPoints)))
	numerator.Add(numerator, big.NewInt(5_000))
	numerator.Div(numerator, big.NewInt(10_000))
	if !numerator.IsInt64() {
		return 0, fmt.Errorf("scaled micros value is too large")
	}
	return numerator.Int64(), nil
}
