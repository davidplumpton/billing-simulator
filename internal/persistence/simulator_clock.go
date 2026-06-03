package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	defaultSimulatorClockCurrentTime = "2026-02-01T00:00:00Z"
	maxSimulatorClockAdvanceAmount   = 1000
)

// SimulatorClockAdvanceUnit identifies supported manual clock increments.
type SimulatorClockAdvanceUnit string

const (
	// SimulatorClockAdvanceHours advances the clock by fixed UTC hours.
	SimulatorClockAdvanceHours SimulatorClockAdvanceUnit = "hours"

	// SimulatorClockAdvanceDays advances the clock by UTC calendar days.
	SimulatorClockAdvanceDays SimulatorClockAdvanceUnit = "days"

	// SimulatorClockAdvanceBillingPeriods advances to later monthly billing-period boundaries.
	SimulatorClockAdvanceBillingPeriods SimulatorClockAdvanceUnit = "billing_periods"
)

// SimulatorClock stores the workspace's deterministic learner-controlled time.
type SimulatorClock struct {
	CurrentTime        string
	BillingPeriodStart string
	BillingPeriodEnd   string
	BillingPeriodDays  int
	UpdatedAt          string
}

// SimulatorClockAdvanceRequest describes a manual clock advance operation.
type SimulatorClockAdvanceRequest struct {
	Amount int
	Unit   SimulatorClockAdvanceUnit
}

// SimulatorClockRepository reads and updates the singleton workspace clock.
type SimulatorClockRepository struct {
	db *sql.DB
}

type simulatorClockQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// NewSimulatorClockRepository creates a repository backed by a workspace database.
func NewSimulatorClockRepository(db *sql.DB) SimulatorClockRepository {
	return SimulatorClockRepository{db: db}
}

// Get returns the current workspace clock and its containing billing period.
func (r SimulatorClockRepository) Get(ctx context.Context) (SimulatorClock, error) {
	if r.db == nil {
		return SimulatorClock{}, fmt.Errorf("database handle is required")
	}
	return readSimulatorClock(ctx, r.db)
}

// Set moves the workspace clock to an exact RFC3339 UTC timestamp.
func (r SimulatorClockRepository) Set(ctx context.Context, currentTime string) (SimulatorClock, error) {
	if r.db == nil {
		return SimulatorClock{}, fmt.Errorf("database handle is required")
	}
	parsed, err := parseSimulatorClockTime(currentTime)
	if err != nil {
		return SimulatorClock{}, err
	}

	var clock SimulatorClock
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE simulator_clock
			 SET current_time_utc = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			 WHERE id = 1`,
			formatSimulatorClockTime(parsed),
		); err != nil {
			return fmt.Errorf("set simulator clock: %w", err)
		}
		var err error
		clock, err = readSimulatorClock(ctx, tx)
		return err
	})
	if err != nil {
		return SimulatorClock{}, err
	}
	return clock, nil
}

// Advance moves the workspace clock forward by a supported deterministic increment.
func (r SimulatorClockRepository) Advance(ctx context.Context, request SimulatorClockAdvanceRequest) (SimulatorClock, error) {
	if r.db == nil {
		return SimulatorClock{}, fmt.Errorf("database handle is required")
	}
	if err := validateSimulatorClockAdvanceRequest(request); err != nil {
		return SimulatorClock{}, err
	}

	var clock SimulatorClock
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		current, err := readSimulatorClock(ctx, tx)
		if err != nil {
			return err
		}
		currentTime, err := parseSimulatorClockTime(current.CurrentTime)
		if err != nil {
			return err
		}
		nextTime := advanceSimulatorClockTime(currentTime, request)
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE simulator_clock
			 SET current_time_utc = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			 WHERE id = 1`,
			formatSimulatorClockTime(nextTime),
		); err != nil {
			return fmt.Errorf("advance simulator clock: %w", err)
		}
		clock, err = readSimulatorClock(ctx, tx)
		return err
	})
	if err != nil {
		return SimulatorClock{}, err
	}
	return clock, nil
}

func readSimulatorClock(ctx context.Context, q simulatorClockQuerier) (SimulatorClock, error) {
	var currentTime, updatedAt string
	if err := q.QueryRowContext(
		ctx,
		`SELECT current_time_utc, updated_at FROM simulator_clock WHERE id = 1`,
	).Scan(&currentTime, &updatedAt); err != nil {
		return SimulatorClock{}, fmt.Errorf("read simulator clock: %w", err)
	}
	parsed, err := parseSimulatorClockTime(currentTime)
	if err != nil {
		return SimulatorClock{}, fmt.Errorf("read simulator clock: %w", err)
	}
	period, err := BillingPeriodForTime(parsed)
	if err != nil {
		return SimulatorClock{}, err
	}
	return SimulatorClock{
		CurrentTime:        formatSimulatorClockTime(parsed),
		BillingPeriodStart: period.Start,
		BillingPeriodEnd:   period.End,
		BillingPeriodDays:  period.Days,
		UpdatedAt:          updatedAt,
	}, nil
}

func parseSimulatorClockTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("simulator clock time must use RFC3339: %w", err)
	}
	return parsed.UTC().Truncate(time.Second), nil
}

func formatSimulatorClockTime(value time.Time) string {
	return value.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func validateSimulatorClockAdvanceRequest(request SimulatorClockAdvanceRequest) error {
	if request.Amount <= 0 {
		return fmt.Errorf("clock advance amount must be greater than zero")
	}
	if request.Amount > maxSimulatorClockAdvanceAmount {
		return fmt.Errorf("clock advance amount must be %d or fewer", maxSimulatorClockAdvanceAmount)
	}
	switch request.Unit {
	case SimulatorClockAdvanceHours, SimulatorClockAdvanceDays, SimulatorClockAdvanceBillingPeriods:
		return nil
	default:
		return fmt.Errorf("unsupported clock advance unit %q", request.Unit)
	}
}

func advanceSimulatorClockTime(current time.Time, request SimulatorClockAdvanceRequest) time.Time {
	current = current.UTC()
	switch request.Unit {
	case SimulatorClockAdvanceHours:
		return current.Add(time.Duration(request.Amount) * time.Hour)
	case SimulatorClockAdvanceDays:
		return current.AddDate(0, 0, request.Amount)
	case SimulatorClockAdvanceBillingPeriods:
		periodStart := time.Date(current.Year(), current.Month(), 1, 0, 0, 0, 0, time.UTC)
		return periodStart.AddDate(0, request.Amount, 0)
	default:
		return current
	}
}
