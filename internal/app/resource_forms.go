package app

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func (h resourceLabHandler) resourceFormDefaults(ctx context.Context) (resourceFormDefaults, error) {
	defaults := defaultResourceFormDefaults()
	if h.db == nil {
		return defaults, nil
	}
	clock, err := h.clock.Get(ctx)
	if err != nil {
		return resourceFormDefaults{}, err
	}
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return resourceFormDefaults{}, fmt.Errorf("parse simulator clock: %w", err)
	}
	return resourceFormDefaultsForTime(parsed), nil
}

func resourceCreateRequestFromForm(r *http.Request, defaults resourceFormDefaults) (persistence.ResourceCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.ResourceCreateRequest{}, fmt.Errorf("parse resource form: %w", err)
	}
	preset, ok := resourcePresetByKey(r.PostForm.Get("service_preset"))
	if !ok {
		return persistence.ResourceCreateRequest{}, fmt.Errorf("unknown resource service preset")
	}
	status := strings.TrimSpace(r.PostForm.Get("status"))
	startedAt := ""
	if status != "planned" {
		parsed, err := parseFormTimestamp(r.PostForm.Get("started_at"), defaults.UsageStartRFC3339)
		if err != nil {
			return persistence.ResourceCreateRequest{}, err
		}
		startedAt = parsed
	}

	resourceName := strings.TrimSpace(r.PostForm.Get("resource_name"))
	if resourceName == "" {
		resourceName = preset.DefaultName
	}
	size := strings.TrimSpace(r.PostForm.Get("size"))
	if size == "" {
		size = preset.DefaultSize
	}
	attributes := copyStringMap(preset.Attributes)
	attributes["size"] = size
	attributes["display_service"] = preset.ServiceName

	tags, err := parseTagsText(r.PostForm.Get("tags"))
	if err != nil {
		return persistence.ResourceCreateRequest{}, err
	}

	return persistence.ResourceCreateRequest{
		AccountID:    r.PostForm.Get("account_id"),
		RegionCode:   r.PostForm.Get("region_code"),
		ServiceCode:  preset.ServiceCode,
		ResourceType: preset.ResourceType,
		ResourceName: resourceName,
		Status:       status,
		StartedAt:    startedAt,
		Attributes:   attributes,
		Tags:         tags,
	}, nil
}

func usageEventCreateRequestFromForm(r *http.Request, defaults resourceFormDefaults) (persistence.UsageEventCreateRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.UsageEventCreateRequest{}, fmt.Errorf("parse usage form: %w", err)
	}
	preset, ok := usagePresetByKey(r.PostForm.Get("usage_preset"))
	if !ok {
		return persistence.UsageEventCreateRequest{}, fmt.Errorf("unknown usage preset")
	}
	start, err := parseFormTimestamp(r.PostForm.Get("usage_start_time"), defaults.UsageStartRFC3339)
	if err != nil {
		return persistence.UsageEventCreateRequest{}, err
	}
	end, err := parseFormTimestamp(r.PostForm.Get("usage_end_time"), defaults.UsageEndRFC3339)
	if err != nil {
		return persistence.UsageEventCreateRequest{}, err
	}
	quantityMicros, err := parseQuantityMicros(r.PostForm.Get("quantity"))
	if err != nil {
		return persistence.UsageEventCreateRequest{}, err
	}

	return persistence.UsageEventCreateRequest{
		ResourceID:          r.PostForm.Get("resource_id"),
		ServiceCode:         preset.ServiceCode,
		UsageType:           preset.UsageType,
		Operation:           preset.Operation,
		RegionCode:          preset.RegionCode,
		UsageStartTime:      start,
		UsageEndTime:        end,
		UsageQuantityMicros: quantityMicros,
		UsageUnit:           preset.Unit,
		Attributes: map[string]string{
			"generation": preset.Label,
		},
	}, nil
}

func usageGenerationRequestFromForm(r *http.Request, defaults resourceFormDefaults) (persistence.UsageGenerationRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.UsageGenerationRequest{}, fmt.Errorf("parse usage generation form: %w", err)
	}
	startDate := strings.TrimSpace(r.PostForm.Get("generation_start_date"))
	if startDate == "" {
		startDate = defaults.GenerationStartDate
	}
	days := defaultUsageGenerationDaySpan
	if rawDays := strings.TrimSpace(r.PostForm.Get("generation_days")); rawDays != "" {
		parsedDays, err := strconv.Atoi(rawDays)
		if err != nil {
			return persistence.UsageGenerationRequest{}, fmt.Errorf("generation days must be a whole number: %w", err)
		}
		days = parsedDays
	}

	return persistence.UsageGenerationRequest{
		ResourceID: r.PostForm.Get("resource_id"),
		Pattern:    persistence.UsageGenerationPattern(r.PostForm.Get("generation_pattern")),
		StartDate:  startDate,
		Days:       days,
	}, nil
}

func clockAdvanceRequestFromForm(r *http.Request) (persistence.SimulatorClockAdvanceRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.SimulatorClockAdvanceRequest{}, fmt.Errorf("parse clock form: %w", err)
	}
	amount := defaultClockAdvanceAmount
	if rawAmount := strings.TrimSpace(r.PostForm.Get("clock_advance_amount")); rawAmount != "" {
		parsedAmount, err := strconv.Atoi(rawAmount)
		if err != nil {
			return persistence.SimulatorClockAdvanceRequest{}, fmt.Errorf("clock advance amount must be a whole number: %w", err)
		}
		amount = parsedAmount
	}
	return persistence.SimulatorClockAdvanceRequest{
		Amount: amount,
		Unit:   persistence.SimulatorClockAdvanceUnit(r.PostForm.Get("clock_advance_unit")),
	}, nil
}

func billingPipelineRequestFromForm(r *http.Request) (persistence.BillLineItemGenerationRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.BillLineItemGenerationRequest{}, fmt.Errorf("parse billing pipeline form: %w", err)
	}
	return persistence.BillLineItemGenerationRequest{
		PayerAccountID: r.PostForm.Get("payer_account_id"),
	}, nil
}

func dailyMeteringJobRequestFromForm(r *http.Request, trigger persistence.DailyMeteringJobTrigger) (persistence.DailyMeteringJobRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.DailyMeteringJobRequest{}, fmt.Errorf("parse daily metering form: %w", err)
	}
	return persistence.DailyMeteringJobRequest{
		Trigger:        trigger,
		PayerAccountID: r.PostForm.Get("payer_account_id"),
	}, nil
}

func monthEndCloseRequestFromForm(r *http.Request) (persistence.MonthEndCloseRequest, error) {
	if err := r.ParseForm(); err != nil {
		return persistence.MonthEndCloseRequest{}, fmt.Errorf("parse month-end close form: %w", err)
	}
	dueDays := 0
	if rawDueDays := strings.TrimSpace(r.PostForm.Get("invoice_due_days")); rawDueDays != "" {
		parsedDueDays, err := strconv.Atoi(rawDueDays)
		if err != nil {
			return persistence.MonthEndCloseRequest{}, fmt.Errorf("invoice due days must be a whole number: %w", err)
		}
		dueDays = parsedDueDays
	}
	return persistence.MonthEndCloseRequest{
		PayerAccountID: r.PostForm.Get("payer_account_id"),
		InvoiceDueDays: dueDays,
	}, nil
}

func resourcePresets() []resourcePreset {
	return []resourcePreset{
		{
			Key:          "ec2_t3_medium",
			Label:        "Amazon EC2 / t3.medium",
			ServiceCode:  "AmazonEC2",
			ServiceName:  "Amazon EC2",
			ResourceType: "ec2_instance",
			DefaultSize:  "t3.medium",
			DefaultName:  "Storefront web",
			Attributes:   map[string]string{"instance_type": "t3.medium", "operating_system": "linux", "tenancy": "shared"},
		},
		{
			Key:          "s3_standard",
			Label:        "Amazon S3 / Standard bucket",
			ServiceCode:  "AmazonS3",
			ServiceName:  "Amazon S3",
			ResourceType: "s3_bucket",
			DefaultSize:  "standard",
			DefaultName:  "storefront-assets",
			Attributes:   map[string]string{"storage_class": "standard"},
		},
		{
			Key:          "rds_t3_medium",
			Label:        "Amazon RDS / db.t3.medium",
			ServiceCode:  "AmazonRDS",
			ServiceName:  "Amazon RDS",
			ResourceType: "rds_instance",
			DefaultSize:  "db.t3.medium",
			DefaultName:  "Orders database",
			Attributes:   map[string]string{"instance_class": "db.t3.medium", "engine": "postgres"},
		},
		{
			Key:          "nat_gateway",
			Label:        "NAT Gateway / shared gateway",
			ServiceCode:  "AmazonVPCNATGateway",
			ServiceName:  "NAT Gateway",
			ResourceType: "nat_gateway",
			DefaultSize:  "standard",
			DefaultName:  "Shared egress",
			Attributes:   map[string]string{"network_role": "egress"},
		},
		{
			Key:          "lambda_512",
			Label:        "AWS Lambda / 512 MB function",
			ServiceCode:  "AWSLambda",
			ServiceName:  "AWS Lambda",
			ResourceType: "lambda_function",
			DefaultSize:  "512 MB",
			DefaultName:  "Image processor",
			Attributes:   map[string]string{"memory_mb": "512", "runtime": "go"},
		},
		{
			Key:          "data_transfer_path",
			Label:        "AWS Data Transfer / internet path",
			ServiceCode:  "AWSDataTransfer",
			ServiceName:  "AWS Data Transfer",
			ResourceType: "data_transfer_path",
			DefaultSize:  "internet",
			DefaultName:  "Internet egress path",
			Attributes:   map[string]string{"path": "internet"},
		},
	}
}

func usagePresets() []usagePreset {
	return []usagePreset{
		{Key: "ec2_hours", Label: "EC2 instance hours", ServiceCode: "AmazonEC2", UsageType: "instance-hours:t3.medium", Operation: "RunInstances", Unit: "Hours"},
		{Key: "s3_storage", Label: "S3 storage GB-days", ServiceCode: "AmazonS3", UsageType: "storage:standard-gb-month", Operation: "StandardStorage", Unit: "GBDay"},
		{Key: "s3_put", Label: "S3 PUT requests", ServiceCode: "AmazonS3", UsageType: "requests:put-1k", Operation: "PutObject", Unit: "Request"},
		{Key: "s3_get", Label: "S3 GET requests", ServiceCode: "AmazonS3", UsageType: "requests:get-1k", Operation: "GetObject", Unit: "Request"},
		{Key: "lambda_requests", Label: "Lambda requests", ServiceCode: "AWSLambda", UsageType: "requests:lambda-1m", Operation: "Invoke", Unit: "Request"},
		{Key: "lambda_gb_seconds", Label: "Lambda GB-seconds", ServiceCode: "AWSLambda", UsageType: "compute:lambda-gb-second", Operation: "Invoke", Unit: "GBSecond"},
		{Key: "rds_hours", Label: "RDS instance hours", ServiceCode: "AmazonRDS", UsageType: "instance-hours:db.t3.medium", Operation: "CreateDBInstance", Unit: "Hours"},
		{Key: "rds_storage", Label: "RDS storage GB-days", ServiceCode: "AmazonRDS", UsageType: "storage:rds-gp3-gb-month", Operation: "DatabaseStorage", Unit: "GBDay"},
		{Key: "nat_hours", Label: "NAT Gateway hours", ServiceCode: "AmazonVPCNATGateway", UsageType: "nat-gateway-hours", Operation: "NatGateway", Unit: "Hours"},
		{Key: "nat_data", Label: "NAT Gateway GB processed", ServiceCode: "AmazonVPCNATGateway", UsageType: "nat-gateway-data-processed-gb", Operation: "NatGatewayDataProcessing", Unit: "GB"},
		{Key: "data_transfer_out", Label: "Internet data transfer GB", ServiceCode: "AWSDataTransfer", UsageType: "data-transfer-out-internet-gb", Operation: "DataTransferOut", RegionCode: "global", Unit: "GB"},
	}
}

func usageGenerationPresets() []usageGenerationPreset {
	options := persistence.UsageGenerationPatternOptions()
	presets := make([]usageGenerationPreset, 0, len(options))
	for _, option := range options {
		presets = append(presets, usageGenerationPreset{
			Key:   option.Key,
			Label: option.Label,
		})
	}
	return presets
}

func clockAdvanceUnitOptions() []clockAdvanceUnitView {
	return []clockAdvanceUnitView{
		{Key: persistence.SimulatorClockAdvanceHours, Label: "Hours"},
		{Key: persistence.SimulatorClockAdvanceDays, Label: "Days"},
		{Key: persistence.SimulatorClockAdvanceBillingPeriods, Label: "Billing Periods"},
	}
}

func defaultResourceFormDefaults() resourceFormDefaults {
	start, err := time.Parse(time.RFC3339, defaultUsageStartRFC3339)
	if err != nil {
		return resourceFormDefaults{
			UsageStartLocal:     defaultUsageStartLocal,
			UsageEndLocal:       defaultUsageEndLocal,
			UsageStartRFC3339:   defaultUsageStartRFC3339,
			UsageEndRFC3339:     defaultUsageEndRFC3339,
			GenerationStartDate: defaultGenerationStartDate,
		}
	}
	return resourceFormDefaultsForTime(start)
}

func resourceFormDefaultsForTime(value time.Time) resourceFormDefaults {
	start := value.UTC().Truncate(time.Minute)
	end := start.Add(time.Hour)
	return resourceFormDefaults{
		UsageStartLocal:     start.Format("2006-01-02T15:04"),
		UsageEndLocal:       end.Format("2006-01-02T15:04"),
		UsageStartRFC3339:   start.Format(time.RFC3339),
		UsageEndRFC3339:     end.Format(time.RFC3339),
		GenerationStartDate: start.Format(time.DateOnly),
	}
}

func applyClockToResourcePageData(data *resourcePageData, clock persistence.SimulatorClock) {
	data.ClockCurrentTime = clock.CurrentTime
	data.ClockBillingPeriod = fmt.Sprintf(
		"%s to %s (%d days)",
		clock.BillingPeriodStart,
		clock.BillingPeriodEnd,
		clock.BillingPeriodDays,
	)
	parsed, err := time.Parse(time.RFC3339, clock.CurrentTime)
	if err != nil {
		return
	}
	defaults := resourceFormDefaultsForTime(parsed)
	data.DefaultUsageStart = defaults.UsageStartLocal
	data.DefaultUsageEnd = defaults.UsageEndLocal
	data.DefaultGenerationStartDate = defaults.GenerationStartDate
}

func resourcePresetByKey(key string) (resourcePreset, bool) {
	key = strings.TrimSpace(key)
	for _, preset := range resourcePresets() {
		if preset.Key == key {
			return preset, true
		}
	}
	return resourcePreset{}, false
}

func usagePresetByKey(key string) (usagePreset, bool) {
	key = strings.TrimSpace(key)
	for _, preset := range usagePresets() {
		if preset.Key == key {
			return preset, true
		}
	}
	return usagePreset{}, false
}

func parseTagsText(raw string) (map[string]string, error) {
	tags := map[string]string{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ','
	}) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			key, value, ok = strings.Cut(part, ":")
		}
		if !ok {
			return nil, fmt.Errorf("tag %q must use key=value", part)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("tag key is required")
		}
		if _, exists := tags[key]; exists {
			return nil, fmt.Errorf("duplicate tag key %q", key)
		}
		tags[key] = strings.TrimSpace(value)
	}
	return tags, nil
}

func parseFormTimestamp(value, defaultValue string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultValue
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC().Format(time.RFC3339), nil
	}
	parsed, err := time.Parse("2006-01-02T15:04", value)
	if err != nil {
		return "", fmt.Errorf("time must use YYYY-MM-DDTHH:MM or RFC3339: %w", err)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func parseQuantityMicros(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("usage quantity is required")
	}
	quantity, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("usage quantity must be numeric: %w", err)
	}
	if quantity <= 0 {
		return 0, fmt.Errorf("usage quantity must be greater than zero")
	}
	quantityMicros := math.Round(quantity * 1_000_000)
	if quantityMicros > float64(math.MaxInt64) {
		return 0, fmt.Errorf("usage quantity is too large")
	}
	return int64(quantityMicros), nil
}

func copyStringMap(values map[string]string) map[string]string {
	copied := map[string]string{}
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
