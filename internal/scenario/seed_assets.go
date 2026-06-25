package scenario

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

const (
	// FirstConsolidatedBillSeedKey identifies the packaged consolidated-billing starter lab.
	FirstConsolidatedBillSeedKey = "first-consolidated-bill"

	// MissingTagsSeedKey identifies the packaged cost allocation tag cleanup lab.
	MissingTagsSeedKey = "missing-tags"

	// SharedNetworkingAllocationSeedKey identifies the packaged shared networking allocation lab.
	SharedNetworkingAllocationSeedKey = "shared-networking-allocation"

	// PaymentFailureSeedKey identifies the packaged failed-payment remediation lab.
	PaymentFailureSeedKey = "payment-failure"

	// ForecastBudgetAlertSeedKey identifies the packaged budget forecast alert lab.
	ForecastBudgetAlertSeedKey = "forecast-budget-alert"

	// SavingsPlanCoverageSeedKey identifies the packaged Savings Plan coverage lab.
	SavingsPlanCoverageSeedKey = "savings-plan-coverage"

	// CostExplorerVarianceInvestigationSeedKey identifies the packaged month-over-month variance lab.
	CostExplorerVarianceInvestigationSeedKey = "cost-explorer-variance-investigation"

	// UntaggedDataTransferSpikeSeedKey identifies the packaged MVP scenario fixture.
	UntaggedDataTransferSpikeSeedKey = "untagged-data-transfer-spike"
)

// scenarioSeedFS contains packaged scenario definitions embedded into the binary.
//
//go:embed seeds/*.json
var scenarioSeedFS embed.FS

// SeedDefinitionKeys returns deterministic keys for packaged scenario fixtures.
func SeedDefinitionKeys() ([]string, error) {
	entries, err := fs.ReadDir(scenarioSeedFS, "seeds")
	if err != nil {
		return nil, fmt.Errorf("read packaged scenario seeds: %w", err)
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		keys = append(keys, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(keys)
	return keys, nil
}

// LoadSeedDefinition parses one packaged scenario fixture by stable key.
func LoadSeedDefinition(key string) (Definition, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, `/\.`) {
		return Definition{}, fmt.Errorf("scenario seed key %q is invalid", key)
	}
	raw, err := scenarioSeedFS.ReadFile(path.Join("seeds", key+".json"))
	if err != nil {
		return Definition{}, fmt.Errorf("read packaged scenario seed %q: %w", key, err)
	}
	definition, err := ParseDefinitionBytes(raw)
	if err != nil {
		return Definition{}, fmt.Errorf("parse packaged scenario seed %q: %w", key, err)
	}
	return definition, nil
}
