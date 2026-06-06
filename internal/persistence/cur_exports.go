package persistence

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	defaultCURLineItemLimit = 100
	maxCURLineItemLimit     = 10_000
)

var curCSVExportColumns = append([]string{
	"export_generated_at",
	"source_bill_id",
}, curLineItemColumns...)

var curLineItemColumns = []string{
	"line_item_id",
	"billing_period_start",
	"billing_period_end",
	"payer_account_id",
	"usage_account_id",
	"account_name",
	"service_code",
	"service_name",
	"product_code",
	"region",
	"availability_zone",
	"usage_type",
	"operation",
	"line_item_type",
	"resource_id",
	"usage_start_time",
	"usage_end_time",
	"usage_amount",
	"usage_unit",
	"unblended_rate",
	"unblended_cost",
	"currency",
	"legal_entity",
	"invoice_entity",
	"tags_json",
	"cost_categories_json",
	"description",
}

// CURLineItem stores one bill line item projected into the simulator's CUR-like export schema.
type CURLineItem struct {
	LineItemID          string
	BillingPeriodStart  string
	BillingPeriodEnd    string
	PayerAccountID      string
	UsageAccountID      string
	AccountName         string
	ServiceCode         string
	ServiceName         string
	ProductCode         string
	Region              string
	AvailabilityZone    string
	UsageType           string
	Operation           string
	LineItemType        string
	ResourceID          string
	UsageStartTime      string
	UsageEndTime        string
	UsageAmountMicros   int64
	UsageUnit           string
	UnblendedRateMicros int64
	UnblendedCostMicros int64
	Currency            string
	LegalEntity         string
	InvoiceEntity       string
	Tags                map[string]string
	CostCategories      map[string]string
	Description         string
}

// CURLineItemListRequest filters bill line items for CUR-like export reads.
type CURLineItemListRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	UsageAccountID     string
	LineItemStatus     string
	Limit              int
}

// CURCSVExportRequest selects the payer-period line items written to a CUR-like CSV export.
type CURCSVExportRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	PayerAccountID     string
	UsageAccountID     string
	LineItemStatus     string
	GeneratedAt        string
	Limit              int
}

// CURCSVExportResult reports deterministic metadata attached to one CSV export.
type CURCSVExportResult struct {
	GeneratedAt  string
	SourceBillID string
	RowsWritten  int
}

// CURLineItemRepository maps persisted bill line items to CUR-like export rows.
type CURLineItemRepository struct {
	db *sql.DB
}

// NewCURLineItemRepository creates a CUR-like export mapper backed by a workspace database.
func NewCURLineItemRepository(db *sql.DB) CURLineItemRepository {
	return CURLineItemRepository{db: db}
}

// CURLineItemColumns returns the stable column order used by CUR-like line-item exports.
func CURLineItemColumns() []string {
	columns := make([]string, len(curLineItemColumns))
	copy(columns, curLineItemColumns)
	return columns
}

// CURCSVExportColumns returns the stable column order for downloadable CUR-like CSV exports.
func CURCSVExportColumns() []string {
	columns := make([]string, len(curCSVExportColumns))
	copy(columns, curCSVExportColumns)
	return columns
}

// ListLineItems reads bill line items and projects them into the CUR-like export schema.
func (r CURLineItemRepository) ListLineItems(ctx context.Context, request CURLineItemListRequest) ([]CURLineItem, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeCURLineItemListRequest(request)
	if err := validateCURLineItemListRequest(request); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			li.id,
			li.billing_period_start,
			li.billing_period_end,
			li.payer_account_id,
			li.usage_account_id,
			COALESCE(oa.name, ''),
			li.service_code,
			li.service_name,
			li.service_code,
			li.region_code,
			'',
			li.usage_type,
			li.operation,
			li.line_item_type,
			li.resource_id,
			li.usage_start_time,
			li.usage_end_time,
			li.pricing_quantity_micros,
			li.pricing_unit,
			li.unblended_rate_micros,
			li.unblended_cost_micros,
			li.currency_code,
			COALESCE(NULLIF(doc.seller_of_record, ''), NULLIF(seller.seller_of_record, ''), ?),
			COALESCE(NULLIF(doc.seller_of_record, ''), NULLIF(seller.seller_of_record, ''), ?),
			li.tag_snapshot_json,
			li.description
		 FROM bill_line_items li
		 LEFT JOIN accounts oa ON oa.id = li.usage_account_id
		 LEFT JOIN invoice_documents doc
		   ON doc.billing_period_start = li.billing_period_start
		  AND doc.billing_period_end = li.billing_period_end
		  AND doc.payer_account_id = li.payer_account_id
		  AND doc.currency_code = li.currency_code
		 LEFT JOIN payment_profiles profile
		   ON profile.payer_account_id = li.payer_account_id
		  AND profile.currency_code = li.currency_code
		  AND profile.status = ?
		  AND profile.is_default = 1
		 LEFT JOIN payment_seller_profiles seller
		   ON seller.id = profile.seller_profile_id
		  AND seller.status = ?
		 WHERE (? = '' OR li.billing_period_start = ?)
		   AND (? = '' OR li.billing_period_end = ?)
		   AND (? = '' OR li.payer_account_id = ?)
		   AND (? = '' OR li.usage_account_id = ?)
		   AND (? = '' OR li.line_item_status = ?)
		 ORDER BY li.billing_period_start, li.billing_period_end, li.usage_start_time, li.id
		 LIMIT ?`,
		defaultInvoiceSellerOfRecord,
		defaultInvoiceSellerOfRecord,
		paymentProfileStatusActive,
		paymentSellerProfileStatusActive,
		request.BillingPeriodStart,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BillingPeriodEnd,
		request.PayerAccountID,
		request.PayerAccountID,
		request.UsageAccountID,
		request.UsageAccountID,
		request.LineItemStatus,
		request.LineItemStatus,
		request.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list CUR-like line items: %w", err)
	}
	defer rows.Close()

	items := []CURLineItem{}
	lineItemIDs := []string{}
	for rows.Next() {
		item, err := scanCURLineItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		lineItemIDs = append(lineItemIDs, item.LineItemID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CUR-like line items: %w", err)
	}
	if len(items) == 0 {
		return items, nil
	}

	categories, err := r.costCategoriesForLineItems(ctx, lineItemIDs)
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].CostCategories = categories[items[i].LineItemID]
		if items[i].CostCategories == nil {
			items[i].CostCategories = map[string]string{}
		}
	}
	return items, nil
}

// WriteCSVExport streams a deterministic payer-period CUR-like CSV export to writer.
func (r CURLineItemRepository) WriteCSVExport(ctx context.Context, writer io.Writer, request CURCSVExportRequest) (CURCSVExportResult, error) {
	if r.db == nil {
		return CURCSVExportResult{}, fmt.Errorf("database handle is required")
	}
	if writer == nil {
		return CURCSVExportResult{}, fmt.Errorf("CUR-like CSV export writer is required")
	}
	request = normalizeCURCSVExportRequest(request)
	if err := validateCURCSVExportRequest(request); err != nil {
		return CURCSVExportResult{}, err
	}

	generatedAt, err := r.resolveCURCSVExportGeneratedAt(ctx, request.GeneratedAt)
	if err != nil {
		return CURCSVExportResult{}, err
	}
	sourceBillID, err := r.sourceBillIDForCURCSVExport(ctx, request)
	if err != nil {
		return CURCSVExportResult{}, err
	}
	items, err := r.ListLineItems(ctx, CURLineItemListRequest{
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		PayerAccountID:     request.PayerAccountID,
		UsageAccountID:     request.UsageAccountID,
		LineItemStatus:     request.LineItemStatus,
		Limit:              request.Limit,
	})
	if err != nil {
		return CURCSVExportResult{}, err
	}

	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write(CURCSVExportColumns()); err != nil {
		return CURCSVExportResult{}, err
	}
	for _, item := range items {
		record, err := curCSVExportRecord(generatedAt, sourceBillID, item)
		if err != nil {
			return CURCSVExportResult{}, err
		}
		if err := csvWriter.Write(record); err != nil {
			return CURCSVExportResult{}, err
		}
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return CURCSVExportResult{}, err
	}

	return CURCSVExportResult{
		GeneratedAt:  generatedAt,
		SourceBillID: sourceBillID,
		RowsWritten:  len(items),
	}, nil
}

func normalizeCURLineItemListRequest(request CURLineItemListRequest) CURLineItemListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.UsageAccountID = strings.TrimSpace(request.UsageAccountID)
	request.LineItemStatus = strings.TrimSpace(request.LineItemStatus)
	if request.Limit <= 0 {
		request.Limit = defaultCURLineItemLimit
	}
	if request.Limit > maxCURLineItemLimit {
		request.Limit = maxCURLineItemLimit
	}
	return request
}

func validateCURLineItemListRequest(request CURLineItemListRequest) error {
	if (request.BillingPeriodStart == "") != (request.BillingPeriodEnd == "") {
		return fmt.Errorf("CUR-like export billing period start and end must be provided together")
	}
	if request.BillingPeriodStart != "" {
		if _, err := time.Parse(time.DateOnly, request.BillingPeriodStart); err != nil {
			return fmt.Errorf("CUR-like export billing period start must use YYYY-MM-DD: %w", err)
		}
		if _, err := time.Parse(time.DateOnly, request.BillingPeriodEnd); err != nil {
			return fmt.Errorf("CUR-like export billing period end must use YYYY-MM-DD: %w", err)
		}
		if request.BillingPeriodStart >= request.BillingPeriodEnd {
			return fmt.Errorf("CUR-like export billing period start must be before end")
		}
	}
	if request.LineItemStatus != "" && !isBillLineItemStatus(request.LineItemStatus) {
		return fmt.Errorf("unsupported CUR-like export line item status %q", request.LineItemStatus)
	}
	return nil
}

func normalizeCURCSVExportRequest(request CURCSVExportRequest) CURCSVExportRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.UsageAccountID = strings.TrimSpace(request.UsageAccountID)
	request.LineItemStatus = strings.TrimSpace(request.LineItemStatus)
	request.GeneratedAt = strings.TrimSpace(request.GeneratedAt)
	if request.Limit <= 0 || request.Limit > maxCURLineItemLimit {
		request.Limit = maxCURLineItemLimit
	}
	return request
}

func validateCURCSVExportRequest(request CURCSVExportRequest) error {
	if request.BillingPeriodStart == "" || request.BillingPeriodEnd == "" {
		return fmt.Errorf("CUR-like CSV export billing period start and end are required")
	}
	if request.PayerAccountID == "" {
		return fmt.Errorf("CUR-like CSV export payer account ID is required")
	}
	if err := validateCURLineItemListRequest(CURLineItemListRequest{
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		LineItemStatus:     request.LineItemStatus,
		Limit:              request.Limit,
	}); err != nil {
		return err
	}
	if request.GeneratedAt != "" {
		if _, err := time.Parse(time.RFC3339, request.GeneratedAt); err != nil {
			return fmt.Errorf("CUR-like CSV export generation time must use RFC3339: %w", err)
		}
	}
	return nil
}

func (r CURLineItemRepository) resolveCURCSVExportGeneratedAt(ctx context.Context, value string) (string, error) {
	if value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			return "", fmt.Errorf("CUR-like CSV export generation time must use RFC3339: %w", err)
		}
		return parsed.UTC().Format(time.RFC3339), nil
	}

	clock, err := NewSimulatorClockRepository(r.db).Get(ctx)
	if err != nil {
		return "", fmt.Errorf("read simulator clock for CUR-like CSV export: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return "", fmt.Errorf("parse simulator clock for CUR-like CSV export: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func (r CURLineItemRepository) sourceBillIDForCURCSVExport(ctx context.Context, request CURCSVExportRequest) (string, error) {
	var sourceBillID string
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id
		 FROM bills
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		 ORDER BY currency_code, id
		 LIMIT 1`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.PayerAccountID,
	).Scan(&sourceBillID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read source bill for CUR-like CSV export: %w", err)
	}
	return sourceBillID, nil
}

func scanCURLineItem(row interface{ Scan(dest ...any) error }) (CURLineItem, error) {
	var item CURLineItem
	var resourceID sql.NullString
	var tagsJSON string
	if err := row.Scan(
		&item.LineItemID,
		&item.BillingPeriodStart,
		&item.BillingPeriodEnd,
		&item.PayerAccountID,
		&item.UsageAccountID,
		&item.AccountName,
		&item.ServiceCode,
		&item.ServiceName,
		&item.ProductCode,
		&item.Region,
		&item.AvailabilityZone,
		&item.UsageType,
		&item.Operation,
		&item.LineItemType,
		&resourceID,
		&item.UsageStartTime,
		&item.UsageEndTime,
		&item.UsageAmountMicros,
		&item.UsageUnit,
		&item.UnblendedRateMicros,
		&item.UnblendedCostMicros,
		&item.Currency,
		&item.LegalEntity,
		&item.InvoiceEntity,
		&tagsJSON,
		&item.Description,
	); err != nil {
		return CURLineItem{}, fmt.Errorf("scan CUR-like line item: %w", err)
	}
	item.ResourceID = nullStringValue(resourceID)
	tags, err := unmarshalStringMap(tagsJSON)
	if err != nil {
		return CURLineItem{}, fmt.Errorf("decode CUR-like tags for line item %q: %w", item.LineItemID, err)
	}
	item.Tags = tags
	item.CostCategories = map[string]string{}
	return item, nil
}

func (r CURLineItemRepository) costCategoriesForLineItems(ctx context.Context, lineItemIDs []string) (map[string]map[string]string, error) {
	categories := make(map[string]map[string]string, len(lineItemIDs))
	placeholders := make([]string, 0, len(lineItemIDs))
	args := make([]any, 0, len(lineItemIDs))
	for _, id := range lineItemIDs {
		categories[id] = map[string]string{}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT line_item_id, cost_category_name, assigned_value
		 FROM cost_category_line_item_assignments
		 WHERE line_item_id IN (`+strings.Join(placeholders, ",")+`)
		 ORDER BY line_item_id, lower(cost_category_name), cost_category_name`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list CUR-like cost categories: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var lineItemID, name, value string
		if err := rows.Scan(&lineItemID, &name, &value); err != nil {
			return nil, fmt.Errorf("scan CUR-like cost category: %w", err)
		}
		if _, ok := categories[lineItemID]; ok {
			categories[lineItemID][name] = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate CUR-like cost categories: %w", err)
	}
	return categories, nil
}

func curCSVExportRecord(generatedAt, sourceBillID string, item CURLineItem) ([]string, error) {
	tagsJSON, err := marshalStringMap(item.Tags)
	if err != nil {
		return nil, fmt.Errorf("encode CUR-like tags for line item %q: %w", item.LineItemID, err)
	}
	costCategoriesJSON, err := marshalStringMap(item.CostCategories)
	if err != nil {
		return nil, fmt.Errorf("encode CUR-like cost categories for line item %q: %w", item.LineItemID, err)
	}
	return []string{
		generatedAt,
		sourceBillID,
		item.LineItemID,
		item.BillingPeriodStart,
		item.BillingPeriodEnd,
		item.PayerAccountID,
		item.UsageAccountID,
		item.AccountName,
		item.ServiceCode,
		item.ServiceName,
		item.ProductCode,
		item.Region,
		item.AvailabilityZone,
		item.UsageType,
		item.Operation,
		item.LineItemType,
		item.ResourceID,
		item.UsageStartTime,
		item.UsageEndTime,
		formatCURMicrosDecimal(item.UsageAmountMicros),
		item.UsageUnit,
		formatCURMicrosDecimal(item.UnblendedRateMicros),
		formatCURMicrosDecimal(item.UnblendedCostMicros),
		item.Currency,
		item.LegalEntity,
		item.InvoiceEntity,
		tagsJSON,
		costCategoriesJSON,
		item.Description,
	}, nil
}

func formatCURMicrosDecimal(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%06d", sign, value/1_000_000, value%1_000_000)
}
