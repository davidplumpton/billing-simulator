package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestSimulatorClockRepositoryAdvancesDeterministically(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSimulatorClockRepository(db)

	clock, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertSimulatorClock(t, clock, "2026-02-01T00:00:00Z", "2026-02-01", "2026-03-01", 28)

	clock, err = repo.Advance(ctx, SimulatorClockAdvanceRequest{
		Amount: 6,
		Unit:   SimulatorClockAdvanceHours,
	})
	if err != nil {
		t.Fatalf("Advance(hours) error = %v", err)
	}
	assertSimulatorClock(t, clock, "2026-02-01T06:00:00Z", "2026-02-01", "2026-03-01", 28)

	clock, err = repo.Advance(ctx, SimulatorClockAdvanceRequest{
		Amount: 2,
		Unit:   SimulatorClockAdvanceDays,
	})
	if err != nil {
		t.Fatalf("Advance(days) error = %v", err)
	}
	assertSimulatorClock(t, clock, "2026-02-03T06:00:00Z", "2026-02-01", "2026-03-01", 28)

	clock, err = repo.Advance(ctx, SimulatorClockAdvanceRequest{
		Amount: 1,
		Unit:   SimulatorClockAdvanceBillingPeriods,
	})
	if err != nil {
		t.Fatalf("Advance(billing periods) error = %v", err)
	}
	assertSimulatorClock(t, clock, "2026-03-01T00:00:00Z", "2026-03-01", "2026-04-01", 31)
}

func TestSimulatorClockRepositorySetAndValidation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewSimulatorClockRepository(db)

	clock, err := repo.Set(ctx, "2028-02-29T12:34:56Z")
	if err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	assertSimulatorClock(t, clock, "2028-02-29T12:34:56Z", "2028-02-01", "2028-03-01", 29)

	if _, err := repo.Set(ctx, "2028-02-29"); err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("Set(invalid) error = %v, want RFC3339 validation", err)
	}
	if _, err := repo.Advance(ctx, SimulatorClockAdvanceRequest{Amount: 0, Unit: SimulatorClockAdvanceHours}); err == nil {
		t.Fatal("Advance(zero amount) error = nil, want validation error")
	}
	if _, err := repo.Advance(ctx, SimulatorClockAdvanceRequest{Amount: 1, Unit: "fortnights"}); err == nil {
		t.Fatal("Advance(unsupported unit) error = nil, want validation error")
	}
}

func assertSimulatorClock(t *testing.T, clock SimulatorClock, wantCurrent, wantStart, wantEnd string, wantDays int) {
	t.Helper()

	if clock.CurrentTime != wantCurrent ||
		clock.BillingPeriodStart != wantStart ||
		clock.BillingPeriodEnd != wantEnd ||
		clock.BillingPeriodDays != wantDays ||
		clock.UpdatedAt == "" {
		t.Fatalf(
			"simulator clock = %+v, want current %s period %s/%s days %d with updated_at",
			clock,
			wantCurrent,
			wantStart,
			wantEnd,
			wantDays,
		)
	}
}
