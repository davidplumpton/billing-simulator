package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	defaultCostExplorerGranularity = "monthly"
	costExplorerMissingGroupValue  = "(none)"
)

// CostExplorerQueryRequest selects bill line items for Cost Explorer-style aggregation.
type CostExplorerQueryRequest struct {
	DateRangeStart string
	DateRangeEnd   string
	Granularity    string
	Filters        map[string][]string
	Groupings      []CostExplorerGrouping
}

// CostExplorerGrouping describes one dimension, tag, or Cost Category grouping.
type CostExplorerGrouping = SavedReportGrouping

// CostExplorerQueryResult reports aggregate spend and usage for one query.
type CostExplorerQueryResult struct {
	DateRangeStart           string
	DateRangeEnd             string
	Granularity              string
	TotalLineItemCount       int
	TotalUsageQuantityMicros int64
	TotalUnblendedCostMicros int64
	Rows                     []CostExplorerQueryRow
}

// CostExplorerQueryRow stores one time bucket and grouping combination.
type CostExplorerQueryRow struct {
	TimePeriodStart     string
	TimePeriodEnd       string
	GroupValues         []CostExplorerGroupValue
	CurrencyCode        string
	LineItemCount       int
	UsageQuantityMicros int64
	UnblendedCostMicros int64
}

// CostExplorerGroupValue names one grouping value on an aggregate row.
type CostExplorerGroupValue struct {
	Type  string
	Key   string
	Value string
}

// CostExplorerRepository aggregates priced bill line items for Cost Explorer workflows.
type CostExplorerRepository struct {
	db *sql.DB
}

// NewCostExplorerRepository creates a Cost Explorer repository backed by a workspace database.
func NewCostExplorerRepository(db *sql.DB) CostExplorerRepository {
	return CostExplorerRepository{db: db}
}

// Query aggregates bill line items by time bucket and up to two requested groupings.
func (r CostExplorerRepository) Query(ctx context.Context, request CostExplorerQueryRequest) (CostExplorerQueryResult, error) {
	if r.db == nil {
		return CostExplorerQueryResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostExplorerQueryRequest(request)
	resolved, err := r.resolveQuery(ctx, request)
	if err != nil {
		return CostExplorerQueryResult{}, err
	}

	rows, err := r.queryRows(ctx, resolved)
	if err != nil {
		return CostExplorerQueryResult{}, err
	}
	result := CostExplorerQueryResult{
		DateRangeStart: request.DateRangeStart,
		DateRangeEnd:   request.DateRangeEnd,
		Granularity:    request.Granularity,
		Rows:           rows,
	}
	for _, row := range rows {
		result.TotalLineItemCount += row.LineItemCount
		result.TotalUsageQuantityMicros += row.UsageQuantityMicros
		result.TotalUnblendedCostMicros += row.UnblendedCostMicros
	}
	return result, nil
}

type costExplorerResolvedQuery struct {
	request   CostExplorerQueryRequest
	startUTC  string
	endUTC    string
	filters   []costExplorerFilterSpec
	groupings []costExplorerGroupingSpec
}

type costExplorerFilterSpec struct {
	Type        string
	Key         string
	ResolvedKey string
	Values      []string
}

type costExplorerGroupingSpec struct {
	Type        string
	Key         string
	ResolvedKey string
}

func normalizeCostExplorerQueryRequest(request CostExplorerQueryRequest) CostExplorerQueryRequest {
	request.DateRangeStart = strings.TrimSpace(request.DateRangeStart)
	request.DateRangeEnd = strings.TrimSpace(request.DateRangeEnd)
	request.Granularity = strings.TrimSpace(request.Granularity)
	if request.Granularity == "" {
		request.Granularity = defaultCostExplorerGranularity
	}
	request.Filters = normalizeSavedReportFilters(request.Filters)
	request.Groupings = normalizeSavedReportGroupings(request.Groupings)
	return request
}

func (r CostExplorerRepository) resolveQuery(ctx context.Context, request CostExplorerQueryRequest) (costExplorerResolvedQuery, error) {
	start, end, err := validateCostExplorerDateRange(request.DateRangeStart, request.DateRangeEnd)
	if err != nil {
		return costExplorerResolvedQuery{}, err
	}
	if err := validateCostExplorerGranularity(request.Granularity); err != nil {
		return costExplorerResolvedQuery{}, err
	}
	filters, err := r.resolveFilters(ctx, request.Filters)
	if err != nil {
		return costExplorerResolvedQuery{}, err
	}
	groupings, err := r.resolveGroupings(ctx, request.Groupings)
	if err != nil {
		return costExplorerResolvedQuery{}, err
	}
	return costExplorerResolvedQuery{
		request:   request,
		startUTC:  start.Format(time.RFC3339),
		endUTC:    end.Format(time.RFC3339),
		filters:   filters,
		groupings: groupings,
	}, nil
}

func validateCostExplorerDateRange(startDate, endDate string) (time.Time, time.Time, error) {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("cost explorer date range start must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("cost explorer date range end must use YYYY-MM-DD: %w", err)
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("cost explorer date range start must be before end")
	}
	return start.UTC(), end.UTC(), nil
}

func validateCostExplorerGranularity(granularity string) error {
	switch granularity {
	case "hourly", "daily", "monthly":
		return nil
	default:
		return fmt.Errorf("cost explorer granularity %q is not supported", granularity)
	}
}

func (r CostExplorerRepository) resolveFilters(ctx context.Context, filters map[string][]string) ([]costExplorerFilterSpec, error) {
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	resolved := make([]costExplorerFilterSpec, 0, len(keys))
	for _, rawKey := range keys {
		filterType, key, err := parseCostExplorerFilterKey(rawKey)
		if err != nil {
			return nil, err
		}
		values, err := validateCostExplorerFilterValues(rawKey, filters[rawKey])
		if err != nil {
			return nil, err
		}
		spec := costExplorerFilterSpec{
			Type:   filterType,
			Key:    key,
			Values: values,
		}
		switch filterType {
		case "dimension":
			if _, err := costExplorerDimensionExpression(key); err != nil {
				return nil, err
			}
			spec.ResolvedKey = key
		case "tag":
			spec.ResolvedKey = key
		case "cost_category":
			category, err := resolveCostExplorerCostCategory(ctx, r.db, key)
			if err != nil {
				return nil, err
			}
			spec.ResolvedKey = category.ID
		}
		resolved = append(resolved, spec)
	}
	return resolved, nil
}

func parseCostExplorerFilterKey(rawKey string) (string, string, error) {
	key := strings.TrimSpace(rawKey)
	if key == "" {
		return "", "", fmt.Errorf("cost explorer filter key is required")
	}
	if strings.HasPrefix(key, "tag:") {
		tagKey := strings.TrimSpace(strings.TrimPrefix(key, "tag:"))
		if tagKey == "" {
			return "", "", fmt.Errorf("cost explorer tag filter key is required")
		}
		return "tag", tagKey, nil
	}
	if strings.HasPrefix(key, "cost_category:") {
		categoryKey := strings.TrimSpace(strings.TrimPrefix(key, "cost_category:"))
		if categoryKey == "" {
			return "", "", fmt.Errorf("cost explorer cost category filter key is required")
		}
		return "cost_category", categoryKey, nil
	}
	return "dimension", key, nil
}

func validateCostExplorerFilterValues(key string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("cost explorer filter %q needs at least one value", key)
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("cost explorer filter %q value is required", key)
		}
		if seen[value] {
			return nil, fmt.Errorf("cost explorer filter %q has duplicate value %q", key, value)
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func (r CostExplorerRepository) resolveGroupings(ctx context.Context, groupings []CostExplorerGrouping) ([]costExplorerGroupingSpec, error) {
	if len(groupings) > 2 {
		return nil, fmt.Errorf("cost explorer supports at most two groupings")
	}
	seen := map[string]bool{}
	resolved := make([]costExplorerGroupingSpec, 0, len(groupings))
	for i, grouping := range groupings {
		if grouping.Key == "" {
			return nil, fmt.Errorf("cost explorer grouping %d key is required", i)
		}
		switch grouping.Type {
		case "dimension", "tag", "cost_category":
		default:
			return nil, fmt.Errorf("cost explorer grouping %d type %q is not supported", i, grouping.Type)
		}
		identity := grouping.Type + ":" + grouping.Key
		if seen[identity] {
			return nil, fmt.Errorf("cost explorer grouping %q is duplicated", identity)
		}
		seen[identity] = true

		spec := costExplorerGroupingSpec{
			Type: grouping.Type,
			Key:  grouping.Key,
		}
		switch grouping.Type {
		case "dimension":
			if _, err := costExplorerDimensionExpression(grouping.Key); err != nil {
				return nil, err
			}
			spec.ResolvedKey = grouping.Key
		case "tag":
			spec.ResolvedKey = grouping.Key
		case "cost_category":
			category, err := resolveCostExplorerCostCategory(ctx, r.db, grouping.Key)
			if err != nil {
				return nil, err
			}
			spec.ResolvedKey = category.ID
		}
		resolved = append(resolved, spec)
	}
	return resolved, nil
}

func (r CostExplorerRepository) queryRows(ctx context.Context, query costExplorerResolvedQuery) ([]CostExplorerQueryRow, error) {
	bucketExpression, err := costExplorerBucketExpression(query.request.Granularity)
	if err != nil {
		return nil, err
	}
	groupExpressions := []string{"''", "''"}
	selectArgs := []any{}
	for i, grouping := range query.groupings {
		expression, args, err := costExplorerGroupingExpression(grouping)
		if err != nil {
			return nil, err
		}
		groupExpressions[i] = expression
		selectArgs = append(selectArgs, args...)
	}

	whereClauses := []string{
		"bli.usage_start_time >= ?",
		"bli.usage_start_time < ?",
	}
	whereArgs := []any{query.startUTC, query.endUTC}
	for _, filter := range query.filters {
		condition, args, err := costExplorerFilterCondition(filter)
		if err != nil {
			return nil, err
		}
		whereClauses = append(whereClauses, condition)
		whereArgs = append(whereArgs, args...)
	}

	sqlQuery := fmt.Sprintf(
		`SELECT
			bucket_start,
			currency_code,
			group_1_value,
			group_2_value,
			COUNT(*),
			COALESCE(SUM(usage_quantity_micros), 0),
			COALESCE(SUM(unblended_cost_micros), 0)
		 FROM (
			SELECT
				%s AS bucket_start,
				bli.currency_code AS currency_code,
				%s AS group_1_value,
				%s AS group_2_value,
				bli.usage_quantity_micros AS usage_quantity_micros,
				bli.unblended_cost_micros AS unblended_cost_micros
			FROM bill_line_items bli
			WHERE %s
		 ) q
		 GROUP BY bucket_start, currency_code, group_1_value, group_2_value
		 ORDER BY bucket_start, group_1_value, group_2_value, currency_code`,
		bucketExpression,
		groupExpressions[0],
		groupExpressions[1],
		strings.Join(whereClauses, " AND "),
	)
	args := append(selectArgs, whereArgs...)
	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query cost explorer aggregates: %w", err)
	}
	defer rows.Close()

	var resultRows []CostExplorerQueryRow
	for rows.Next() {
		var row CostExplorerQueryRow
		var group1, group2 string
		if err := rows.Scan(
			&row.TimePeriodStart,
			&row.CurrencyCode,
			&group1,
			&group2,
			&row.LineItemCount,
			&row.UsageQuantityMicros,
			&row.UnblendedCostMicros,
		); err != nil {
			return nil, fmt.Errorf("scan cost explorer aggregate: %w", err)
		}
		row.TimePeriodEnd, err = costExplorerBucketEnd(query.request.Granularity, row.TimePeriodStart)
		if err != nil {
			return nil, err
		}
		row.GroupValues = costExplorerGroupValues(query.groupings, group1, group2)
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost explorer aggregates: %w", err)
	}
	return resultRows, nil
}

func costExplorerBucketExpression(granularity string) (string, error) {
	switch granularity {
	case "hourly":
		return "strftime('%Y-%m-%dT%H:00:00Z', bli.usage_start_time)", nil
	case "daily":
		return "substr(bli.usage_start_time, 1, 10)", nil
	case "monthly":
		return "substr(bli.usage_start_time, 1, 7) || '-01'", nil
	default:
		return "", fmt.Errorf("cost explorer granularity %q is not supported", granularity)
	}
}

func costExplorerBucketEnd(granularity, bucketStart string) (string, error) {
	switch granularity {
	case "hourly":
		start, err := time.Parse(time.RFC3339, bucketStart)
		if err != nil {
			return "", fmt.Errorf("cost explorer hourly bucket %q is invalid: %w", bucketStart, err)
		}
		return start.Add(time.Hour).Format(time.RFC3339), nil
	case "daily":
		start, err := time.Parse("2006-01-02", bucketStart)
		if err != nil {
			return "", fmt.Errorf("cost explorer daily bucket %q is invalid: %w", bucketStart, err)
		}
		return start.AddDate(0, 0, 1).Format("2006-01-02"), nil
	case "monthly":
		start, err := time.Parse("2006-01-02", bucketStart)
		if err != nil {
			return "", fmt.Errorf("cost explorer monthly bucket %q is invalid: %w", bucketStart, err)
		}
		return start.AddDate(0, 1, 0).Format("2006-01-02"), nil
	default:
		return "", fmt.Errorf("cost explorer granularity %q is not supported", granularity)
	}
}

func costExplorerGroupingExpression(grouping costExplorerGroupingSpec) (string, []any, error) {
	switch grouping.Type {
	case "dimension":
		expression, err := costExplorerDimensionExpression(grouping.ResolvedKey)
		return expression, nil, err
	case "tag":
		return `COALESCE((
			SELECT CAST(j.value AS TEXT)
			FROM json_each(bli.tag_snapshot_json) j
			WHERE j.key = ?
			LIMIT 1
		), ?)`, []any{grouping.ResolvedKey, costExplorerMissingGroupValue}, nil
	case "cost_category":
		return `COALESCE((
			SELECT a.assigned_value
			FROM cost_category_line_item_assignments a
			WHERE a.line_item_id = bli.id
			  AND a.cost_category_id = ?
			LIMIT 1
		), ?)`, []any{grouping.ResolvedKey, costExplorerMissingGroupValue}, nil
	default:
		return "", nil, fmt.Errorf("cost explorer grouping type %q is not supported", grouping.Type)
	}
}

func costExplorerFilterCondition(filter costExplorerFilterSpec) (string, []any, error) {
	placeholders := costExplorerPlaceholders(len(filter.Values))
	switch filter.Type {
	case "dimension":
		if filter.ResolvedKey == "service" {
			args := make([]any, 0, len(filter.Values)*2)
			for _, value := range filter.Values {
				args = append(args, value)
			}
			for _, value := range filter.Values {
				args = append(args, value)
			}
			return fmt.Sprintf("(bli.service_code IN (%s) OR bli.service_name IN (%s))", placeholders, placeholders), args, nil
		}
		expression, err := costExplorerDimensionExpression(filter.ResolvedKey)
		if err != nil {
			return "", nil, err
		}
		args := make([]any, 0, len(filter.Values))
		for _, value := range filter.Values {
			args = append(args, value)
		}
		return fmt.Sprintf("%s IN (%s)", expression, placeholders), args, nil
	case "tag":
		args := make([]any, 0, len(filter.Values)+1)
		args = append(args, filter.ResolvedKey)
		for _, value := range filter.Values {
			args = append(args, value)
		}
		return fmt.Sprintf(`EXISTS (
			SELECT 1
			FROM json_each(bli.tag_snapshot_json) j
			WHERE j.key = ?
			  AND CAST(j.value AS TEXT) IN (%s)
		)`, placeholders), args, nil
	case "cost_category":
		args := make([]any, 0, len(filter.Values)+1)
		args = append(args, filter.ResolvedKey)
		for _, value := range filter.Values {
			args = append(args, value)
		}
		return fmt.Sprintf(`EXISTS (
			SELECT 1
			FROM cost_category_line_item_assignments a
			WHERE a.line_item_id = bli.id
			  AND a.cost_category_id = ?
			  AND a.assigned_value IN (%s)
		)`, placeholders), args, nil
	default:
		return "", nil, fmt.Errorf("cost explorer filter type %q is not supported", filter.Type)
	}
}

func costExplorerDimensionExpression(key string) (string, error) {
	switch key {
	case "service":
		return "bli.service_code", nil
	case "linked_account":
		return "bli.usage_account_id", nil
	case "region":
		return "bli.region_code", nil
	case "usage_type":
		return "bli.usage_type", nil
	case "line_item_type":
		return "bli.line_item_type", nil
	default:
		return "", fmt.Errorf("cost explorer dimension %q is not supported", key)
	}
}

func costExplorerPlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func costExplorerGroupValues(groupings []costExplorerGroupingSpec, group1, group2 string) []CostExplorerGroupValue {
	rawValues := []string{group1, group2}
	values := make([]CostExplorerGroupValue, 0, len(groupings))
	for i, grouping := range groupings {
		values = append(values, CostExplorerGroupValue{
			Type:  grouping.Type,
			Key:   grouping.Key,
			Value: rawValues[i],
		})
	}
	return values
}

func resolveCostExplorerCostCategory(ctx context.Context, db *sql.DB, key string) (CostCategory, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return CostCategory{}, fmt.Errorf("cost category ID or name is required")
	}
	category, err := scanCostExplorerCostCategory(db.QueryRowContext(
		ctx,
		`SELECT id, name, description, default_value, status, created_at, updated_at
		 FROM cost_categories
		 WHERE id = ?`,
		key,
	))
	if err == nil {
		return category, nil
	}
	if err != sql.ErrNoRows {
		return CostCategory{}, fmt.Errorf("resolve cost category %q: %w", key, err)
	}
	category, err = scanCostExplorerCostCategory(db.QueryRowContext(
		ctx,
		`SELECT id, name, description, default_value, status, created_at, updated_at
		 FROM cost_categories
		 WHERE lower(name) = lower(?)`,
		key,
	))
	if err != nil {
		if err == sql.ErrNoRows {
			return CostCategory{}, fmt.Errorf("cost category %q does not exist", key)
		}
		return CostCategory{}, fmt.Errorf("resolve cost category %q: %w", key, err)
	}
	return category, nil
}

func scanCostExplorerCostCategory(row costCategoryRow) (CostCategory, error) {
	var category CostCategory
	if err := row.Scan(
		&category.ID,
		&category.Name,
		&category.Description,
		&category.DefaultValue,
		&category.Status,
		&category.CreatedAt,
		&category.UpdatedAt,
	); err != nil {
		return CostCategory{}, err
	}
	return category, nil
}
