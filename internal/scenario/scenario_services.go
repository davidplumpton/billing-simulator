package scenario

import (
	"fmt"
	"strings"
)

type scenarioServiceDefaults struct {
	ServiceCode         string
	ServiceName         string
	ResourceType        string
	DefaultResourceName string
	RegionCode          string
	UsageType           string
	Operation           string
	UsageUnit           string
	Attributes          map[string]string
}

func scenarioServiceDefaultsForEvent(event Event) (scenarioServiceDefaults, error) {
	serviceCode := strings.TrimSpace(event.ServiceCode)
	if serviceCode == "" && event.Service != "" {
		serviceCode = scenarioServiceCodeForName(event.Service)
	}
	if serviceCode == "" {
		if event.Service != "" {
			return scenarioServiceDefaults{}, fmt.Errorf("scenario service %q is not supported", event.Service)
		}
		return scenarioServiceDefaults{}, fmt.Errorf("scenario service is required")
	}
	if defaults, ok := scenarioServiceDefaultsByCode()[serviceCode]; ok {
		return defaults, nil
	}
	if event.UsageType != "" && event.Operation != "" && event.Unit != "" {
		return scenarioServiceDefaults{
			ServiceCode:         serviceCode,
			ServiceName:         chooseFirst(event.Service, serviceCode),
			ResourceType:        chooseFirst(event.ResourceType, "scenario_resource"),
			DefaultResourceName: chooseFirst(event.Resource, serviceCode+" scenario resource"),
			RegionCode:          chooseFirst(event.Region, "us-east-1"),
			UsageType:           event.UsageType,
			Operation:           event.Operation,
			UsageUnit:           event.Unit,
		}, nil
	}
	return scenarioServiceDefaults{}, fmt.Errorf("scenario service %q is not supported", chooseFirst(event.Service, event.ServiceCode))
}

func scenarioServiceCodeForName(name string) string {
	return scenarioServiceNameAliases()[scenarioLookupKey(name)]
}

func scenarioServiceDefaultsByCode() map[string]scenarioServiceDefaults {
	return map[string]scenarioServiceDefaults{
		"AmazonEC2": {
			ServiceCode:         "AmazonEC2",
			ServiceName:         "Amazon EC2",
			ResourceType:        "ec2_instance",
			DefaultResourceName: "Scenario EC2 instance",
			RegionCode:          "us-east-1",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageUnit:           "Hours",
			Attributes:          map[string]string{"instance_type": "t3.medium", "operating_system": "linux", "tenancy": "shared"},
		},
		"AmazonEBS": {
			ServiceCode:         "AmazonEBS",
			ServiceName:         "Amazon EBS",
			ResourceType:        "ebs_volume",
			DefaultResourceName: "Scenario gp3 volume",
			RegionCode:          "us-east-1",
			UsageType:           "storage:gp3-gb-month",
			Operation:           "VolumeStorage",
			UsageUnit:           "GBDay",
			Attributes:          map[string]string{"volume_type": "gp3", "size": "100 GB"},
		},
		"AmazonS3": {
			ServiceCode:         "AmazonS3",
			ServiceName:         "Amazon S3",
			ResourceType:        "s3_bucket",
			DefaultResourceName: "Scenario bucket",
			RegionCode:          "us-east-1",
			UsageType:           "storage:standard-gb-month",
			Operation:           "StandardStorage",
			UsageUnit:           "GBDay",
			Attributes:          map[string]string{"storage_class": "standard", "size": "standard"},
		},
		"AWSLambda": {
			ServiceCode:         "AWSLambda",
			ServiceName:         "AWS Lambda",
			ResourceType:        "lambda_function",
			DefaultResourceName: "Scenario function",
			RegionCode:          "us-east-1",
			UsageType:           "requests:lambda-1m",
			Operation:           "Invoke",
			UsageUnit:           "Request",
			Attributes:          map[string]string{"memory_mb": "512", "runtime": "go"},
		},
		"AmazonRDS": {
			ServiceCode:         "AmazonRDS",
			ServiceName:         "Amazon RDS",
			ResourceType:        "rds_instance",
			DefaultResourceName: "Scenario database",
			RegionCode:          "us-east-1",
			UsageType:           "instance-hours:db.t3.medium",
			Operation:           "CreateDBInstance",
			UsageUnit:           "Hours",
			Attributes:          map[string]string{"instance_class": "db.t3.medium", "engine": "postgres"},
		},
		"AmazonVPCNATGateway": {
			ServiceCode:         "AmazonVPCNATGateway",
			ServiceName:         "NAT Gateway",
			ResourceType:        "nat_gateway",
			DefaultResourceName: "Scenario NAT Gateway",
			RegionCode:          "us-east-1",
			UsageType:           "nat-gateway-data-processed-gb",
			Operation:           "NatGatewayDataProcessing",
			UsageUnit:           "GB",
			Attributes:          map[string]string{"network_role": "egress", "size": "standard"},
		},
		"AWSDataTransfer": {
			ServiceCode:         "AWSDataTransfer",
			ServiceName:         "AWS Data Transfer",
			ResourceType:        "data_transfer_path",
			DefaultResourceName: "Scenario internet egress path",
			RegionCode:          "global",
			UsageType:           "data-transfer-out-internet-gb",
			Operation:           "DataTransferOut",
			UsageUnit:           "GB",
			Attributes:          map[string]string{"path": "internet", "size": "internet"},
		},
		"AmazonCloudWatchLogs": {
			ServiceCode:         "AmazonCloudWatchLogs",
			ServiceName:         "CloudWatch Logs",
			ResourceType:        "log_group",
			DefaultResourceName: "Scenario log group",
			RegionCode:          "us-east-1",
			UsageType:           "logs-ingestion-gb",
			Operation:           "PutLogEvents",
			UsageUnit:           "GB",
			Attributes:          map[string]string{"retention": "standard"},
		},
	}
}

func scenarioServiceNameAliases() map[string]string {
	aliases := map[string]string{}
	for code, defaults := range scenarioServiceDefaultsByCode() {
		aliases[scenarioLookupKey(code)] = code
		aliases[scenarioLookupKey(defaults.ServiceName)] = code
	}
	aliases[scenarioLookupKey("EC2")] = "AmazonEC2"
	aliases[scenarioLookupKey("S3")] = "AmazonS3"
	aliases[scenarioLookupKey("Lambda")] = "AWSLambda"
	aliases[scenarioLookupKey("RDS")] = "AmazonRDS"
	aliases[scenarioLookupKey("NAT Gateway")] = "AmazonVPCNATGateway"
	return aliases
}

func scenarioLookupKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}
