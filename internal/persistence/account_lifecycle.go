package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	defaultAccountLifecycleEventLimit = 50
	maxAccountLifecycleEventLimit     = 200
)

// AccountLifecycleEventType identifies one auditable organization account lifecycle change.
type AccountLifecycleEventType string

const (
	AccountLifecycleEventCreated   AccountLifecycleEventType = "created"
	AccountLifecycleEventMoved     AccountLifecycleEventType = "moved"
	AccountLifecycleEventSuspended AccountLifecycleEventType = "suspended"
	AccountLifecycleEventClosed    AccountLifecycleEventType = "closed"
)

// AccountLifecycleEvent stores the effective-time history needed to interpret account state by billing period.
type AccountLifecycleEvent struct {
	ID                    string
	OrganizationID        string
	AccountID             string
	EventType             AccountLifecycleEventType
	PreviousParentUnitID  string
	NewParentUnitID       string
	PreviousStatus        AccountStatus
	NewStatus             AccountStatus
	EffectiveAt           string
	CreatedAt             string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// AccountLifecycleResult returns the updated account plus the event recorded for the operation.
type AccountLifecycleResult struct {
	Account OrganizationAccount
	Event   AccountLifecycleEvent
}

// AccountCreateRequest describes a new simulated member account joining the organization.
type AccountCreateRequest struct {
	ID                    string
	OrganizationID        string
	ParentUnitID          string
	Name                  string
	Email                 string
	EffectiveAt           string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// AccountMoveRequest describes an OU transfer for an existing simulated account.
type AccountMoveRequest struct {
	AccountID             string
	ParentUnitID          string
	EffectiveAt           string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// AccountSuspendRequest describes a simulated account suspension.
type AccountSuspendRequest struct {
	AccountID             string
	EffectiveAt           string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// AccountCloseRequest describes a simulated account closure.
type AccountCloseRequest struct {
	AccountID             string
	EffectiveAt           string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// CreateAccount inserts an active member account and records its lifecycle baseline event.
func (r OrganizationRepository) CreateAccount(ctx context.Context, request AccountCreateRequest) (AccountLifecycleResult, error) {
	if r.db == nil {
		return AccountLifecycleResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeAccountCreateRequest(request)
	if request.EventSource == "" {
		request.EventSource = "learner"
	}
	if err := validateAccountCreateRequest(request); err != nil {
		return AccountLifecycleResult{}, err
	}
	effectiveAt, err := canonicalAccountLifecycleTimestamp(request.EffectiveAt)
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	request.EffectiveAt = effectiveAt

	eventID, err := newRepositoryID("acct_evt")
	if err != nil {
		return AccountLifecycleResult{}, err
	}

	var event AccountLifecycleEvent
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		organization, err := getOrganizationByIDTx(ctx, tx, request.OrganizationID)
		if err != nil {
			return err
		}
		if _, err := getOrganizationUnitTx(ctx, tx, request.OrganizationID, request.ParentUnitID); err != nil {
			return err
		}
		sortOrder, err := nextAccountSortOrder(ctx, tx, request.OrganizationID)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO accounts (
				id,
				organization_id,
				parent_unit_id,
				name,
				email,
				account_type,
				status,
				created_at,
				joined_at,
				left_at,
				payment_responsibility,
				payer_account_id,
				billing_visibility_role,
				sort_order,
				is_management_account
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?)`,
			request.ID,
			request.OrganizationID,
			request.ParentUnitID,
			request.Name,
			request.Email,
			accountTypeMember,
			AccountStatusActive,
			request.EffectiveAt,
			request.EffectiveAt,
			"management_account",
			organization.ManagementAccountID,
			"member-account",
			sortOrder,
			0,
		); err != nil {
			return fmt.Errorf("insert account %q: %w", request.ID, err)
		}

		event = AccountLifecycleEvent{
			ID:                    eventID,
			OrganizationID:        request.OrganizationID,
			AccountID:             request.ID,
			EventType:             AccountLifecycleEventCreated,
			NewParentUnitID:       request.ParentUnitID,
			NewStatus:             AccountStatusActive,
			EffectiveAt:           request.EffectiveAt,
			EventSource:           request.EventSource,
			ScenarioRunID:         request.ScenarioRunID,
			ScenarioEventID:       request.ScenarioEventID,
			ScenarioEventSequence: request.ScenarioEventSequence,
		}
		return insertAccountLifecycleEvent(ctx, tx, event)
	})
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	return r.lifecycleResult(ctx, request.ID, event.ID)
}

// MoveAccount transfers a member account to another OU and records the effective move event.
func (r OrganizationRepository) MoveAccount(ctx context.Context, request AccountMoveRequest) (AccountLifecycleResult, error) {
	if r.db == nil {
		return AccountLifecycleResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeAccountMoveRequest(request)
	if request.EventSource == "" {
		request.EventSource = "learner"
	}
	if err := validateAccountMoveRequest(request); err != nil {
		return AccountLifecycleResult{}, err
	}
	effectiveAt, err := canonicalAccountLifecycleTimestamp(request.EffectiveAt)
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	request.EffectiveAt = effectiveAt

	eventID, err := newRepositoryID("acct_evt")
	if err != nil {
		return AccountLifecycleResult{}, err
	}

	var event AccountLifecycleEvent
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		account, err := getOrganizationAccountTx(ctx, tx, request.AccountID)
		if err != nil {
			return err
		}
		if err := validateMutableMemberAccount(account, "move"); err != nil {
			return err
		}
		if request.ParentUnitID == account.ParentUnitID {
			return fmt.Errorf("account %q already belongs to OU %q", account.ID, request.ParentUnitID)
		}
		if _, err := getOrganizationUnitTx(ctx, tx, account.OrganizationID, request.ParentUnitID); err != nil {
			return err
		}
		if err := validateAccountLifecycleEffectiveOrder(ctx, tx, account, request.EffectiveAt, false); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE accounts
			   SET parent_unit_id = ?
			 WHERE id = ?`,
			request.ParentUnitID,
			account.ID,
		); err != nil {
			return fmt.Errorf("move account %q: %w", account.ID, err)
		}

		event = AccountLifecycleEvent{
			ID:                    eventID,
			OrganizationID:        account.OrganizationID,
			AccountID:             account.ID,
			EventType:             AccountLifecycleEventMoved,
			PreviousParentUnitID:  account.ParentUnitID,
			NewParentUnitID:       request.ParentUnitID,
			PreviousStatus:        account.Status,
			NewStatus:             account.Status,
			EffectiveAt:           request.EffectiveAt,
			EventSource:           request.EventSource,
			ScenarioRunID:         request.ScenarioRunID,
			ScenarioEventID:       request.ScenarioEventID,
			ScenarioEventSequence: request.ScenarioEventSequence,
		}
		return insertAccountLifecycleEvent(ctx, tx, event)
	})
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	return r.lifecycleResult(ctx, request.AccountID, event.ID)
}

// SuspendAccount marks an active member account suspended and records the effective status change.
func (r OrganizationRepository) SuspendAccount(ctx context.Context, request AccountSuspendRequest) (AccountLifecycleResult, error) {
	if r.db == nil {
		return AccountLifecycleResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeAccountSuspendRequest(request)
	if request.EventSource == "" {
		request.EventSource = "learner"
	}
	if err := validateAccountSuspendRequest(request); err != nil {
		return AccountLifecycleResult{}, err
	}
	effectiveAt, err := canonicalAccountLifecycleTimestamp(request.EffectiveAt)
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	request.EffectiveAt = effectiveAt

	eventID, err := newRepositoryID("acct_evt")
	if err != nil {
		return AccountLifecycleResult{}, err
	}

	var event AccountLifecycleEvent
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		account, err := getOrganizationAccountTx(ctx, tx, request.AccountID)
		if err != nil {
			return err
		}
		if err := validateMutableMemberAccount(account, "suspend"); err != nil {
			return err
		}
		if account.Status != AccountStatusActive {
			return fmt.Errorf("account %q must be active before suspension; current status is %q", account.ID, account.Status)
		}
		if err := validateAccountLifecycleEffectiveOrder(ctx, tx, account, request.EffectiveAt, false); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE accounts
			   SET status = ?
			 WHERE id = ?`,
			AccountStatusSuspended,
			account.ID,
		); err != nil {
			return fmt.Errorf("suspend account %q: %w", account.ID, err)
		}

		event = AccountLifecycleEvent{
			ID:                    eventID,
			OrganizationID:        account.OrganizationID,
			AccountID:             account.ID,
			EventType:             AccountLifecycleEventSuspended,
			PreviousParentUnitID:  account.ParentUnitID,
			NewParentUnitID:       account.ParentUnitID,
			PreviousStatus:        account.Status,
			NewStatus:             AccountStatusSuspended,
			EffectiveAt:           request.EffectiveAt,
			EventSource:           request.EventSource,
			ScenarioRunID:         request.ScenarioRunID,
			ScenarioEventID:       request.ScenarioEventID,
			ScenarioEventSequence: request.ScenarioEventSequence,
		}
		return insertAccountLifecycleEvent(ctx, tx, event)
	})
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	return r.lifecycleResult(ctx, request.AccountID, event.ID)
}

// CloseAccount marks a member account closed, sets its left_at time, and records the closure event.
func (r OrganizationRepository) CloseAccount(ctx context.Context, request AccountCloseRequest) (AccountLifecycleResult, error) {
	if r.db == nil {
		return AccountLifecycleResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeAccountCloseRequest(request)
	if request.EventSource == "" {
		request.EventSource = "learner"
	}
	if err := validateAccountCloseRequest(request); err != nil {
		return AccountLifecycleResult{}, err
	}
	effectiveAt, err := canonicalAccountLifecycleTimestamp(request.EffectiveAt)
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	request.EffectiveAt = effectiveAt

	eventID, err := newRepositoryID("acct_evt")
	if err != nil {
		return AccountLifecycleResult{}, err
	}

	var event AccountLifecycleEvent
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		account, err := getOrganizationAccountTx(ctx, tx, request.AccountID)
		if err != nil {
			return err
		}
		if err := validateMutableMemberAccount(account, "close"); err != nil {
			return err
		}
		if err := validateAccountLifecycleEffectiveOrder(ctx, tx, account, request.EffectiveAt, true); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`UPDATE accounts
			   SET status = ?,
			       left_at = ?
			 WHERE id = ?`,
			AccountStatusClosed,
			request.EffectiveAt,
			account.ID,
		); err != nil {
			return fmt.Errorf("close account %q: %w", account.ID, err)
		}

		event = AccountLifecycleEvent{
			ID:                    eventID,
			OrganizationID:        account.OrganizationID,
			AccountID:             account.ID,
			EventType:             AccountLifecycleEventClosed,
			PreviousParentUnitID:  account.ParentUnitID,
			NewParentUnitID:       account.ParentUnitID,
			PreviousStatus:        account.Status,
			NewStatus:             AccountStatusClosed,
			EffectiveAt:           request.EffectiveAt,
			EventSource:           request.EventSource,
			ScenarioRunID:         request.ScenarioRunID,
			ScenarioEventID:       request.ScenarioEventID,
			ScenarioEventSequence: request.ScenarioEventSequence,
		}
		return insertAccountLifecycleEvent(ctx, tx, event)
	})
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	return r.lifecycleResult(ctx, request.AccountID, event.ID)
}

// ListAccountLifecycleEvents returns recent account lifecycle changes for an organization.
func (r OrganizationRepository) ListAccountLifecycleEvents(ctx context.Context, organizationID string, limit int) ([]AccountLifecycleEvent, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, fmt.Errorf("organization ID is required")
	}
	if limit <= 0 {
		limit = defaultAccountLifecycleEventLimit
	}
	if limit > maxAccountLifecycleEventLimit {
		limit = maxAccountLifecycleEventLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT id,
				organization_id,
				account_id,
				event_type,
				previous_parent_unit_id,
				new_parent_unit_id,
				previous_status,
				new_status,
				effective_at,
				created_at,
				event_source,
				scenario_run_id,
				scenario_event_id,
				scenario_event_sequence
		   FROM account_lifecycle_events
		  WHERE organization_id = ?
		  ORDER BY effective_at DESC, created_at DESC, id DESC
		  LIMIT ?`,
		organizationID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list account lifecycle events for %q: %w", organizationID, err)
	}
	defer rows.Close()

	var events []AccountLifecycleEvent
	for rows.Next() {
		event, err := scanAccountLifecycleEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate account lifecycle events for %q: %w", organizationID, err)
	}
	return events, nil
}

func (r OrganizationRepository) lifecycleResult(ctx context.Context, accountID, eventID string) (AccountLifecycleResult, error) {
	account, err := r.GetAccount(ctx, accountID)
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	event, err := r.getAccountLifecycleEvent(ctx, eventID)
	if err != nil {
		return AccountLifecycleResult{}, err
	}
	return AccountLifecycleResult{Account: account, Event: event}, nil
}

func (r OrganizationRepository) getAccountLifecycleEvent(ctx context.Context, eventID string) (AccountLifecycleEvent, error) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return AccountLifecycleEvent{}, fmt.Errorf("account lifecycle event ID is required")
	}
	event, err := scanAccountLifecycleEvent(r.db.QueryRowContext(
		ctx,
		`SELECT id,
				organization_id,
				account_id,
				event_type,
				previous_parent_unit_id,
				new_parent_unit_id,
				previous_status,
				new_status,
				effective_at,
				created_at,
				event_source,
				scenario_run_id,
				scenario_event_id,
				scenario_event_sequence
		   FROM account_lifecycle_events
		  WHERE id = ?`,
		eventID,
	))
	if err != nil {
		return AccountLifecycleEvent{}, fmt.Errorf("get account lifecycle event %q: %w", eventID, err)
	}
	return event, nil
}

func insertAccountLifecycleEvent(ctx context.Context, tx *sql.Tx, event AccountLifecycleEvent) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO account_lifecycle_events (
			id,
			organization_id,
			account_id,
			event_type,
			previous_parent_unit_id,
			new_parent_unit_id,
			previous_status,
			new_status,
			effective_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.OrganizationID,
		event.AccountID,
		event.EventType,
		nullStringArg(event.PreviousParentUnitID),
		nullStringArg(event.NewParentUnitID),
		nullStringArg(string(event.PreviousStatus)),
		event.NewStatus,
		event.EffectiveAt,
		event.EventSource,
		nullStringArg(event.ScenarioRunID),
		nullStringArg(event.ScenarioEventID),
		nullIntArg(event.ScenarioEventSequence),
	)
	if err != nil {
		return fmt.Errorf("insert account lifecycle event for account %q: %w", event.AccountID, err)
	}
	return nil
}

func getOrganizationByIDTx(ctx context.Context, tx *sql.Tx, organizationID string) (Organization, error) {
	var organization Organization
	if err := tx.QueryRowContext(
		ctx,
		`SELECT id, template_key, name, management_account_id, created_at
		   FROM organizations
		  WHERE id = ?`,
		organizationID,
	).Scan(
		&organization.ID,
		&organization.TemplateKey,
		&organization.Name,
		&organization.ManagementAccountID,
		&organization.CreatedAt,
	); err != nil {
		return Organization{}, fmt.Errorf("get organization %q: %w", organizationID, err)
	}
	return organization, nil
}

func getOrganizationUnitTx(ctx context.Context, tx *sql.Tx, organizationID, unitID string) (OrganizationUnit, error) {
	unit, err := scanOrganizationUnit(tx.QueryRowContext(
		ctx,
		`SELECT id, organization_id, parent_unit_id, name, path, sort_order, created_at
		   FROM organization_units
		  WHERE organization_id = ? AND id = ?`,
		organizationID,
		unitID,
	))
	if err != nil {
		return OrganizationUnit{}, fmt.Errorf("get organization unit %q in organization %q: %w", unitID, organizationID, err)
	}
	return unit, nil
}

func getOrganizationAccountTx(ctx context.Context, tx *sql.Tx, accountID string) (OrganizationAccount, error) {
	account, err := scanOrganizationAccount(tx.QueryRowContext(
		ctx,
		`SELECT id,
				organization_id,
				parent_unit_id,
				ou_path,
				name,
				email,
				account_type,
				status,
				created_at,
				joined_at,
				left_at,
				payment_responsibility,
				payer_account_id,
				billing_visibility_role,
				is_management_account,
				sort_order
		   FROM organization_account_hierarchy
		  WHERE id = ?`,
		accountID,
	))
	if err != nil {
		return OrganizationAccount{}, fmt.Errorf("get organization account %q: %w", accountID, err)
	}
	return account, nil
}

func nextAccountSortOrder(ctx context.Context, tx *sql.Tx, organizationID string) (int, error) {
	var sortOrder int
	if err := tx.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(sort_order), 0) + 10
		   FROM accounts
		  WHERE organization_id = ?`,
		organizationID,
	).Scan(&sortOrder); err != nil {
		return 0, fmt.Errorf("read next account sort order for %q: %w", organizationID, err)
	}
	return sortOrder, nil
}

func validateAccountLifecycleEffectiveOrder(ctx context.Context, tx *sql.Tx, account OrganizationAccount, effectiveAt string, mustBeAfterJoin bool) error {
	effectiveTime, err := time.Parse(time.RFC3339, effectiveAt)
	if err != nil {
		return fmt.Errorf("account lifecycle effective_at must use RFC3339: %w", err)
	}
	joinedAt, err := time.Parse(time.RFC3339, account.JoinedAt)
	if err != nil {
		return fmt.Errorf("account %q joined_at must use RFC3339: %w", account.ID, err)
	}
	if mustBeAfterJoin {
		if !effectiveTime.After(joinedAt) {
			return fmt.Errorf("account %q close time must be after joined_at %s", account.ID, account.JoinedAt)
		}
	} else if effectiveTime.Before(joinedAt) {
		return fmt.Errorf("account %q lifecycle time must not be before joined_at %s", account.ID, account.JoinedAt)
	}

	latestEffectiveAt, ok, err := latestAccountLifecycleEffectiveAt(ctx, tx, account.ID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	latestTime, err := time.Parse(time.RFC3339, latestEffectiveAt)
	if err != nil {
		return fmt.Errorf("account %q latest lifecycle event time must use RFC3339: %w", account.ID, err)
	}
	if effectiveTime.Before(latestTime) {
		return fmt.Errorf("account %q lifecycle time %s must not be before latest event %s", account.ID, effectiveAt, latestEffectiveAt)
	}
	return nil
}

func latestAccountLifecycleEffectiveAt(ctx context.Context, tx *sql.Tx, accountID string) (string, bool, error) {
	var effectiveAt string
	err := tx.QueryRowContext(
		ctx,
		`SELECT effective_at
		   FROM account_lifecycle_events
		  WHERE account_id = ?
		  ORDER BY effective_at DESC, created_at DESC, id DESC
		  LIMIT 1`,
		accountID,
	).Scan(&effectiveAt)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read latest account lifecycle event for %q: %w", accountID, err)
	}
	return effectiveAt, true, nil
}

func normalizeAccountCreateRequest(request AccountCreateRequest) AccountCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.OrganizationID = strings.TrimSpace(request.OrganizationID)
	request.ParentUnitID = strings.TrimSpace(request.ParentUnitID)
	request.Name = strings.TrimSpace(request.Name)
	request.Email = strings.TrimSpace(request.Email)
	request.EffectiveAt = strings.TrimSpace(request.EffectiveAt)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	return request
}

func normalizeAccountMoveRequest(request AccountMoveRequest) AccountMoveRequest {
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.ParentUnitID = strings.TrimSpace(request.ParentUnitID)
	request.EffectiveAt = strings.TrimSpace(request.EffectiveAt)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	return request
}

func normalizeAccountSuspendRequest(request AccountSuspendRequest) AccountSuspendRequest {
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.EffectiveAt = strings.TrimSpace(request.EffectiveAt)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	return request
}

func normalizeAccountCloseRequest(request AccountCloseRequest) AccountCloseRequest {
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.EffectiveAt = strings.TrimSpace(request.EffectiveAt)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	return request
}

func validateAccountCreateRequest(request AccountCreateRequest) error {
	if err := validateOrganizationAccountID("account ID", request.ID); err != nil {
		return err
	}
	if request.OrganizationID == "" {
		return fmt.Errorf("organization ID is required")
	}
	if request.ParentUnitID == "" {
		return fmt.Errorf("parent OU ID is required")
	}
	if request.Name == "" {
		return fmt.Errorf("account name is required")
	}
	if request.Email == "" {
		return fmt.Errorf("account email is required")
	}
	if !strings.Contains(request.Email, "@") {
		return fmt.Errorf("account email must contain @")
	}
	if err := validateRequiredLifecycleTimestamp("account joined_at", request.EffectiveAt); err != nil {
		return err
	}
	return validateEventSourceProvenance("account lifecycle", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence)
}

func validateAccountMoveRequest(request AccountMoveRequest) error {
	if err := validateOrganizationAccountID("account ID", request.AccountID); err != nil {
		return err
	}
	if request.ParentUnitID == "" {
		return fmt.Errorf("target OU ID is required")
	}
	if err := validateRequiredLifecycleTimestamp("account lifecycle effective_at", request.EffectiveAt); err != nil {
		return err
	}
	return validateEventSourceProvenance("account lifecycle", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence)
}

func validateAccountSuspendRequest(request AccountSuspendRequest) error {
	if err := validateOrganizationAccountID("account ID", request.AccountID); err != nil {
		return err
	}
	if err := validateRequiredLifecycleTimestamp("account lifecycle effective_at", request.EffectiveAt); err != nil {
		return err
	}
	return validateEventSourceProvenance("account lifecycle", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence)
}

func validateAccountCloseRequest(request AccountCloseRequest) error {
	if err := validateOrganizationAccountID("account ID", request.AccountID); err != nil {
		return err
	}
	if err := validateRequiredLifecycleTimestamp("account lifecycle effective_at", request.EffectiveAt); err != nil {
		return err
	}
	return validateEventSourceProvenance("account lifecycle", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence)
}

func validateMutableMemberAccount(account OrganizationAccount, action string) error {
	if account.IsManagementAccount {
		return fmt.Errorf("management account %q cannot be %sd", account.ID, action)
	}
	if account.Status == AccountStatusClosed {
		return fmt.Errorf("closed account %q cannot be %sd", account.ID, action)
	}
	return nil
}

func validateRequiredLifecycleTimestamp(label, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if _, err := canonicalAccountLifecycleTimestamp(value); err != nil {
		return fmt.Errorf("%s must use RFC3339: %w", label, err)
	}
	return nil
}

func canonicalAccountLifecycleTimestamp(value string) (string, error) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return parsed.UTC().Truncate(time.Second).Format(time.RFC3339), nil
}

func validateOrganizationAccountID(label, accountID string) error {
	if accountID == "" {
		return fmt.Errorf("%s is required", label)
	}
	if len(accountID) != 12 {
		return fmt.Errorf("%s must be a 12 digit account ID", label)
	}
	for _, char := range accountID {
		if char < '0' || char > '9' {
			return fmt.Errorf("%s must be a 12 digit account ID", label)
		}
	}
	return nil
}

type accountLifecycleEventRow interface {
	Scan(dest ...any) error
}

func scanAccountLifecycleEvent(row accountLifecycleEventRow) (AccountLifecycleEvent, error) {
	var event AccountLifecycleEvent
	var previousParentUnitID, newParentUnitID, previousStatus sql.NullString
	var scenarioRunID, scenarioEventID sql.NullString
	var scenarioEventSequence sql.NullInt64
	if err := row.Scan(
		&event.ID,
		&event.OrganizationID,
		&event.AccountID,
		&event.EventType,
		&previousParentUnitID,
		&newParentUnitID,
		&previousStatus,
		&event.NewStatus,
		&event.EffectiveAt,
		&event.CreatedAt,
		&event.EventSource,
		&scenarioRunID,
		&scenarioEventID,
		&scenarioEventSequence,
	); err != nil {
		return AccountLifecycleEvent{}, fmt.Errorf("scan account lifecycle event: %w", err)
	}
	event.PreviousParentUnitID = nullStringValue(previousParentUnitID)
	event.NewParentUnitID = nullStringValue(newParentUnitID)
	event.PreviousStatus = AccountStatus(nullStringValue(previousStatus))
	event.ScenarioRunID = nullStringValue(scenarioRunID)
	event.ScenarioEventID = nullStringValue(scenarioEventID)
	event.ScenarioEventSequence = nullIntValue(scenarioEventSequence)
	return event, nil
}
