package persistence

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	usageGeneratorQuantityMicros = int64(1_000_000)
	maxUsageGenerationDays       = 31

	serviceAmazonEC2           = "AmazonEC2"
	serviceAmazonEBS           = "AmazonEBS"
	serviceAmazonS3            = "AmazonS3"
	serviceAWSLambda           = "AWSLambda"
	serviceAmazonRDS           = "AmazonRDS"
	serviceAmazonVPCNATGateway = "AmazonVPCNATGateway"
	serviceAWSDataTransfer     = "AWSDataTransfer"
)

// UsageGenerationPattern identifies one deterministic usage shape.
type UsageGenerationPattern string

const (
	// UsageGenerationDailyInstanceHours emits one full-day instance-hour event per day.
	UsageGenerationDailyInstanceHours UsageGenerationPattern = "daily_instance_hours"

	// UsageGenerationStorageGrowth emits one daily GB-day storage event with linear growth.
	UsageGenerationStorageGrowth UsageGenerationPattern = "storage_growth"

	// UsageGenerationRequests emits deterministic daily request-count events.
	UsageGenerationRequests UsageGenerationPattern = "requests"

	// UsageGenerationLambdaExecution emits paired Lambda request and duration events.
	UsageGenerationLambdaExecution UsageGenerationPattern = "lambda_execution"

	// UsageGenerationNATTraffic emits paired NAT Gateway hour and processed-GB events.
	UsageGenerationNATTraffic UsageGenerationPattern = "nat_traffic"

	// UsageGenerationDataTransferSpikes emits daily transfer events with one deterministic spike.
	UsageGenerationDataTransferSpikes UsageGenerationPattern = "data_transfer_spikes"
)

// UsageGenerationPatternOption describes a generator option for UI and scenario fixtures.
type UsageGenerationPatternOption struct {
	Key   UsageGenerationPattern
	Label string
}

// UsageGenerationRequest describes deterministic usage to emit for one resource.
type UsageGenerationRequest struct {
	ResourceID string
	Pattern    UsageGenerationPattern
	StartDate  string
	Days       int
}

// UsageGenerationResult returns the resource snapshot and generated usage events.
type UsageGenerationResult struct {
	Resource Resource
	Events   []UsageEvent
}

type generatedUsageSpec struct {
	DayIndex            int
	Sequence            int
	Date                time.Time
	UsageType           string
	Operation           string
	RegionCode          string
	UsageQuantityMicros int64
	UsageUnit           string
	Attributes          map[string]string
}

// UsageGenerationPatternOptions returns generator choices in learning-workflow order.
func UsageGenerationPatternOptions() []UsageGenerationPatternOption {
	return []UsageGenerationPatternOption{
		{Key: UsageGenerationDailyInstanceHours, Label: "Daily instance hours"},
		{Key: UsageGenerationStorageGrowth, Label: "Storage growth"},
		{Key: UsageGenerationRequests, Label: "Requests"},
		{Key: UsageGenerationLambdaExecution, Label: "Lambda execution"},
		{Key: UsageGenerationNATTraffic, Label: "NAT traffic"},
		{Key: UsageGenerationDataTransferSpikes, Label: "Data transfer spikes"},
	}
}

// GenerateUsage records deterministic usage events for a resource and pattern.
func (r ResourceUsageRepository) GenerateUsage(ctx context.Context, request UsageGenerationRequest) (UsageGenerationResult, error) {
	if r.db == nil {
		return UsageGenerationResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeUsageGenerationRequest(request)
	startDate, err := validateUsageGenerationRequest(request)
	if err != nil {
		return UsageGenerationResult{}, err
	}

	resource, err := r.GetResource(ctx, request.ResourceID)
	if err != nil {
		return UsageGenerationResult{}, err
	}
	specs, err := generatedUsageSpecs(resource, request.Pattern, startDate, request.Days)
	if err != nil {
		return UsageGenerationResult{}, err
	}

	result := UsageGenerationResult{
		Resource: resource,
		Events:   make([]UsageEvent, 0, len(specs)),
	}
	for _, spec := range specs {
		event, err := r.RecordGeneratedUsageEvent(ctx, spec.createRequest(resource.ID, request.Pattern))
		if err != nil {
			return UsageGenerationResult{}, err
		}
		result.Events = append(result.Events, event)
	}
	return result, nil
}

func normalizeUsageGenerationRequest(request UsageGenerationRequest) UsageGenerationRequest {
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.Pattern = UsageGenerationPattern(strings.TrimSpace(string(request.Pattern)))
	request.StartDate = strings.TrimSpace(request.StartDate)
	return request
}

func validateUsageGenerationRequest(request UsageGenerationRequest) (time.Time, error) {
	if request.ResourceID == "" {
		return time.Time{}, fmt.Errorf("usage generation resource ID is required")
	}
	if request.Pattern == "" {
		return time.Time{}, fmt.Errorf("usage generation pattern is required")
	}
	if !isUsageGenerationPattern(request.Pattern) {
		return time.Time{}, fmt.Errorf("unsupported usage generation pattern %q", request.Pattern)
	}
	startDate, err := time.Parse(time.DateOnly, request.StartDate)
	if err != nil {
		return time.Time{}, fmt.Errorf("usage generation start date must use YYYY-MM-DD: %w", err)
	}
	if request.Days <= 0 {
		return time.Time{}, fmt.Errorf("usage generation days must be greater than zero")
	}
	if request.Days > maxUsageGenerationDays {
		return time.Time{}, fmt.Errorf("usage generation days must be %d or fewer", maxUsageGenerationDays)
	}
	return startDate, nil
}

func isUsageGenerationPattern(pattern UsageGenerationPattern) bool {
	for _, option := range UsageGenerationPatternOptions() {
		if option.Key == pattern {
			return true
		}
	}
	return false
}

func generatedUsageSpecs(resource Resource, pattern UsageGenerationPattern, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	switch pattern {
	case UsageGenerationDailyInstanceHours:
		return dailyInstanceHourSpecs(resource, startDate, days)
	case UsageGenerationStorageGrowth:
		return storageGrowthSpecs(resource, startDate, days)
	case UsageGenerationRequests:
		return requestSpecs(resource, startDate, days)
	case UsageGenerationLambdaExecution:
		return lambdaExecutionSpecs(resource, startDate, days)
	case UsageGenerationNATTraffic:
		return natTrafficSpecs(resource, startDate, days)
	case UsageGenerationDataTransferSpikes:
		return dataTransferSpikeSpecs(resource, startDate, days)
	default:
		return nil, fmt.Errorf("unsupported usage generation pattern %q", pattern)
	}
}

func dailyInstanceHourSpecs(resource Resource, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	usageType := ""
	operation := ""
	switch resource.ServiceCode {
	case serviceAmazonEC2:
		usageType = "instance-hours:t3.medium"
		operation = "RunInstances"
	case serviceAmazonRDS:
		usageType = "instance-hours:db.t3.medium"
		operation = "CreateDBInstance"
	default:
		return nil, unsupportedPatternForService(UsageGenerationDailyInstanceHours, resource.ServiceCode)
	}

	specs := make([]generatedUsageSpec, 0, days)
	for day := 0; day < days; day++ {
		specs = append(specs, generatedUsageSpec{
			DayIndex:            day,
			Sequence:            1,
			Date:                startDate.AddDate(0, 0, day),
			UsageType:           usageType,
			Operation:           operation,
			UsageQuantityMicros: quantityMicros(24),
			UsageUnit:           "Hours",
			Attributes: map[string]string{
				"hours_per_day": "24",
			},
		})
	}
	return specs, nil
}

func storageGrowthSpecs(resource Resource, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	usageType := ""
	operation := ""
	baseGB := int64(0)
	growthGB := int64(0)
	switch resource.ServiceCode {
	case serviceAmazonS3:
		usageType = "storage:standard-gb-month"
		operation = "StandardStorage"
		baseGB = 100
		growthGB = 25
	case serviceAmazonRDS:
		usageType = "storage:rds-gp3-gb-month"
		operation = "DatabaseStorage"
		baseGB = 80
		growthGB = 5
	case serviceAmazonEBS:
		usageType = "storage:gp3-gb-month"
		operation = "VolumeStorage"
		baseGB = 100
		growthGB = 10
	default:
		return nil, unsupportedPatternForService(UsageGenerationStorageGrowth, resource.ServiceCode)
	}

	specs := make([]generatedUsageSpec, 0, days)
	for day := 0; day < days; day++ {
		storedGB := baseGB + int64(day)*growthGB
		specs = append(specs, generatedUsageSpec{
			DayIndex:            day,
			Sequence:            1,
			Date:                startDate.AddDate(0, 0, day),
			UsageType:           usageType,
			Operation:           operation,
			UsageQuantityMicros: quantityMicros(storedGB),
			UsageUnit:           "GBDay",
			Attributes: map[string]string{
				"base_gb":         strconv.FormatInt(baseGB, 10),
				"daily_growth_gb": strconv.FormatInt(growthGB, 10),
				"stored_gb":       strconv.FormatInt(storedGB, 10),
			},
		})
	}
	return specs, nil
}

func requestSpecs(resource Resource, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	if resource.ServiceCode != serviceAmazonS3 {
		return nil, unsupportedPatternForService(UsageGenerationRequests, resource.ServiceCode)
	}

	specs := make([]generatedUsageSpec, 0, days*2)
	for day := 0; day < days; day++ {
		date := startDate.AddDate(0, 0, day)
		putRequests := int64(12_000 + day*1_500)
		getRequests := int64(240_000 + day*30_000)
		specs = append(specs,
			generatedUsageSpec{
				DayIndex:            day,
				Sequence:            1,
				Date:                date,
				UsageType:           "requests:put-1k",
				Operation:           "PutObject",
				UsageQuantityMicros: quantityMicros(putRequests),
				UsageUnit:           "Request",
				Attributes: map[string]string{
					"request_family": "write",
					"request_count":  strconv.FormatInt(putRequests, 10),
				},
			},
			generatedUsageSpec{
				DayIndex:            day,
				Sequence:            2,
				Date:                date,
				UsageType:           "requests:get-1k",
				Operation:           "GetObject",
				UsageQuantityMicros: quantityMicros(getRequests),
				UsageUnit:           "Request",
				Attributes: map[string]string{
					"request_family": "read",
					"request_count":  strconv.FormatInt(getRequests, 10),
				},
			},
		)
	}
	return specs, nil
}

func lambdaExecutionSpecs(resource Resource, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	if resource.ServiceCode != serviceAWSLambda {
		return nil, unsupportedPatternForService(UsageGenerationLambdaExecution, resource.ServiceCode)
	}

	specs := make([]generatedUsageSpec, 0, days*2)
	for day := 0; day < days; day++ {
		date := startDate.AddDate(0, 0, day)
		requests := int64(800_000 + day*100_000)
		gbSeconds := requests / 10
		specs = append(specs,
			generatedUsageSpec{
				DayIndex:            day,
				Sequence:            1,
				Date:                date,
				UsageType:           "requests:lambda-1m",
				Operation:           "Invoke",
				UsageQuantityMicros: quantityMicros(requests),
				UsageUnit:           "Request",
				Attributes: map[string]string{
					"request_count": strconv.FormatInt(requests, 10),
				},
			},
			generatedUsageSpec{
				DayIndex:            day,
				Sequence:            2,
				Date:                date,
				UsageType:           "compute:lambda-gb-second",
				Operation:           "Invoke",
				UsageQuantityMicros: quantityMicros(gbSeconds),
				UsageUnit:           "GBSecond",
				Attributes: map[string]string{
					"average_duration_ms": "200",
					"memory_mb":           "512",
				},
			},
		)
	}
	return specs, nil
}

func natTrafficSpecs(resource Resource, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	if resource.ServiceCode != serviceAmazonVPCNATGateway {
		return nil, unsupportedPatternForService(UsageGenerationNATTraffic, resource.ServiceCode)
	}

	specs := make([]generatedUsageSpec, 0, days*2)
	for day := 0; day < days; day++ {
		date := startDate.AddDate(0, 0, day)
		processedGB := int64(75 + day*25)
		specs = append(specs,
			generatedUsageSpec{
				DayIndex:            day,
				Sequence:            1,
				Date:                date,
				UsageType:           "nat-gateway-hours",
				Operation:           "NatGateway",
				UsageQuantityMicros: quantityMicros(24),
				UsageUnit:           "Hours",
				Attributes: map[string]string{
					"hours_per_day": "24",
				},
			},
			generatedUsageSpec{
				DayIndex:            day,
				Sequence:            2,
				Date:                date,
				UsageType:           "nat-gateway-data-processed-gb",
				Operation:           "NatGatewayDataProcessing",
				UsageQuantityMicros: quantityMicros(processedGB),
				UsageUnit:           "GB",
				Attributes: map[string]string{
					"processed_gb": strconv.FormatInt(processedGB, 10),
				},
			},
		)
	}
	return specs, nil
}

func dataTransferSpikeSpecs(resource Resource, startDate time.Time, days int) ([]generatedUsageSpec, error) {
	if resource.ServiceCode != serviceAWSDataTransfer {
		return nil, unsupportedPatternForService(UsageGenerationDataTransferSpikes, resource.ServiceCode)
	}

	spikeDay := days / 2
	specs := make([]generatedUsageSpec, 0, days)
	for day := 0; day < days; day++ {
		transferredGB := int64(35 + day*5)
		spike := "false"
		if day == spikeDay {
			transferredGB = 750
			spike = "true"
		}
		specs = append(specs, generatedUsageSpec{
			DayIndex:            day,
			Sequence:            1,
			Date:                startDate.AddDate(0, 0, day),
			UsageType:           "data-transfer-out-internet-gb",
			Operation:           "DataTransferOut",
			RegionCode:          "global",
			UsageQuantityMicros: quantityMicros(transferredGB),
			UsageUnit:           "GB",
			Attributes: map[string]string{
				"spike":          spike,
				"transferred_gb": strconv.FormatInt(transferredGB, 10),
			},
		})
	}
	return specs, nil
}

func (s generatedUsageSpec) createRequest(resourceID string, pattern UsageGenerationPattern) UsageEventCreateRequest {
	attributes := map[string]string{
		"generation":         usageGenerationPatternLabel(pattern),
		"generator_pattern":  string(pattern),
		"generator_day":      strconv.Itoa(s.DayIndex + 1),
		"generator_sequence": strconv.Itoa(s.Sequence),
	}
	for key, value := range s.Attributes {
		attributes[key] = value
	}
	startTime := s.Date.Format(time.DateOnly) + "T00:00:00Z"
	endTime := s.Date.AddDate(0, 0, 1).Format(time.DateOnly) + "T00:00:00Z"
	return UsageEventCreateRequest{
		ID:                  generatedUsageEventID(resourceID, pattern, s.Date, s.Sequence),
		ResourceID:          resourceID,
		UsageType:           s.UsageType,
		Operation:           s.Operation,
		RegionCode:          s.RegionCode,
		UsageStartTime:      startTime,
		UsageEndTime:        endTime,
		UsageQuantityMicros: s.UsageQuantityMicros,
		UsageUnit:           s.UsageUnit,
		Attributes:          attributes,
	}
}

func usageGenerationPatternLabel(pattern UsageGenerationPattern) string {
	for _, option := range UsageGenerationPatternOptions() {
		if option.Key == pattern {
			return option.Label
		}
	}
	return string(pattern)
}

func generatedUsageEventID(resourceID string, pattern UsageGenerationPattern, date time.Time, sequence int) string {
	return fmt.Sprintf(
		"use_gen_%s_%s_%s_%02d",
		sanitizeGeneratedIDPart(resourceID),
		sanitizeGeneratedIDPart(string(pattern)),
		date.Format("20060102"),
		sequence,
	)
}

func sanitizeGeneratedIDPart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastUnderscore := false
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
			lastUnderscore = false
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	sanitized := strings.Trim(builder.String(), "_")
	if sanitized == "" {
		return "resource"
	}
	return sanitized
}

func unsupportedPatternForService(pattern UsageGenerationPattern, serviceCode string) error {
	return fmt.Errorf("usage generation pattern %q does not support resource service %q", pattern, serviceCode)
}

func quantityMicros(quantity int64) int64 {
	return quantity * usageGeneratorQuantityMicros
}
