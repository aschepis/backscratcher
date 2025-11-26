package agent

import (
	"database/sql"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/rs/zerolog"
)

// State represents the current state of an agent
type State string

const (
	StateIdle            State = "idle"
	StateRunning         State = "running"
	StateWaitingHuman    State = "waiting_human"
	StateWaitingExternal State = "waiting_external"
	StateSleeping        State = "sleeping"
)

// AgentState represents the state of an agent in the database
type AgentState struct {
	AgentID   string
	State     State
	UpdatedAt int64
}

// StateManager manages agent state persistence
type StateManager struct {
	db     *sql.DB
	logger zerolog.Logger
}

// NewStateManager creates a new StateManager
func NewStateManager(logger zerolog.Logger, db *sql.DB) *StateManager {
	return &StateManager{db: db, logger: logger.With().Str("component", "stateManager").Logger()}
}

// StateExists checks if an agent has a persisted state
func (sm *StateManager) StateExists(agentID string) (bool, error) {
	query := sq.Select("COUNT(*)").
		From("agent_states").
		Where(sq.Eq{"agent_id": agentID})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return false, fmt.Errorf("build query: %w", err)
	}

	var count int
	err = sm.db.QueryRow(queryStr, args...).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check if agent state exists: %w", err)
	}
	return count > 0, nil
}

// GetState retrieves the current state of an agent
func (sm *StateManager) GetState(agentID string) (State, error) {
	query := sq.Select("state", "updated_at").
		From("agent_states").
		Where(sq.Eq{"agent_id": agentID})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return "", fmt.Errorf("build query: %w", err)
	}

	var stateStr string
	var updatedAt int64
	err = sm.db.QueryRow(queryStr, args...).Scan(&stateStr, &updatedAt)

	if err == sql.ErrNoRows {
		// Agent has no state yet, return idle as default
		return StateIdle, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to get agent state: %w", err)
	}

	return State(stateStr), nil
}

// SetState updates the state of an agent
func (sm *StateManager) SetState(agentID string, state State) error {
	return sm.SetStateWithNextWake(agentID, state, nil)
}

// SetStateWithNextWake updates the state of an agent and optionally sets next_wake
func (sm *StateManager) SetStateWithNextWake(agentID string, state State, nextWake *time.Time) error {
	now := time.Now().Unix()

	// Validate state
	validStates := []State{StateIdle, StateRunning, StateWaitingHuman, StateWaitingExternal, StateSleeping}
	valid := false
	for _, vs := range validStates {
		if state == vs {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid state: %s", state)
	}

	var nextWakeUnix interface{}
	if nextWake != nil {
		nextWakeUnix = nextWake.Unix()
	} else {
		nextWakeUnix = nil
	}

	query := sq.Insert("agent_states").
		Columns("agent_id", "state", "updated_at", "next_wake").
		Values(agentID, string(state), now, nextWakeUnix).
		Suffix("ON CONFLICT(agent_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at, next_wake = excluded.next_wake")

	queryStr, args, err := query.ToSql()
	if err != nil {
		sm.logger.Error().
			Err(err).
			Str("agentID", agentID).
			Str("state", string(state)).
			Msg("Failed to build query for agent state update")
		return fmt.Errorf("build query: %w", err)
	}

	_, err = sm.db.Exec(queryStr, args...)
	if err != nil {
		sm.logger.Error().
			Err(err).
			Str("agentID", agentID).
			Str("state", string(state)).
			Msg("Failed to set agent state")
		return fmt.Errorf("failed to set agent state: %w", err)
	}

	// Format next_wake for logging: show both Unix timestamp and human-readable time in local timezone
	var nextWakeUnixVal int64
	var nextWakeStr string
	if nextWake != nil {
		nextWakeUnixVal = nextWake.Unix()
		nextWakeStr = nextWake.Format("2006-01-02 15:04:05")
	} else {
		nextWakeStr = "nil"
	}
	sm.logger.Info().
		Str("agentID", agentID).
		Str("state", string(state)).
		Int64("next_wake_unix", nextWakeUnixVal).
		Str("next_wake_human", nextWakeStr).
		Msg("Agent state updated")
	return nil
}

// GetAllStates retrieves all agent states
func (sm *StateManager) GetAllStates() (map[string]State, error) {
	query := sq.Select("agent_id", "state").From("agent_states")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := sm.db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent states: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	states := make(map[string]State)
	for rows.Next() {
		var agentID string
		var stateStr string
		if err := rows.Scan(&agentID, &stateStr); err != nil {
			return nil, fmt.Errorf("failed to scan agent state: %w", err)
		}
		states[agentID] = State(stateStr)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent states: %w", err)
	}

	return states, nil
}

// GetAgentsByState retrieves all agent IDs in a specific state
func (sm *StateManager) GetAgentsByState(state State) ([]string, error) {
	query := sq.Select("agent_id").
		From("agent_states").
		Where(sq.Eq{"state": string(state)})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := sm.db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query agents by state: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var agentIDs []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("failed to scan agent ID: %w", err)
		}
		agentIDs = append(agentIDs, agentID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agents by state: %w", err)
	}

	return agentIDs, nil
}

// SetNextWake sets the next wake time for an agent
func (sm *StateManager) SetNextWake(agentID string, nextWake time.Time) error {
	nextWakeUnix := nextWake.Unix()

	query := sq.Update("agent_states").
		Set("next_wake", nextWakeUnix).
		Where(sq.Eq{"agent_id": agentID})

	queryStr, args, err := query.ToSql()
	if err != nil {
		sm.logger.Error().
			Err(err).
			Str("agentID", agentID).
			Int64("next_wake_unix", nextWakeUnix).
			Msg("Failed to build query for next wake update")
		return fmt.Errorf("build query: %w", err)
	}

	_, err = sm.db.Exec(queryStr, args...)
	if err != nil {
		sm.logger.Error().
			Err(err).
			Str("agentID", agentID).
			Int64("next_wake_unix", nextWakeUnix).
			Msg("Failed to set next wake")
		return fmt.Errorf("failed to set next wake: %w", err)
	}

	sm.logger.Info().
		Str("agentID", agentID).
		Int64("next_wake_unix", nextWakeUnix).
		Str("next_wake_human", nextWake.Format("2006-01-02 15:04:05")).
		Msg("Agent next wake updated")
	return nil
}

// GetNextWake retrieves the next wake time for an agent
func (sm *StateManager) GetNextWake(agentID string) (*time.Time, error) {
	query := sq.Select("next_wake").
		From("agent_states").
		Where(sq.Eq{"agent_id": agentID})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	var nextWakeUnix sql.NullInt64
	err = sm.db.QueryRow(queryStr, args...).Scan(&nextWakeUnix)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get next wake: %w", err)
	}

	if !nextWakeUnix.Valid {
		return nil, nil
	}

	nextWake := time.Unix(nextWakeUnix.Int64, 0)
	return &nextWake, nil
}

// GetAgentsReadyToWake retrieves all agent IDs that are ready to wake
// (state='waiting_external' AND next_wake <= NOW())
func (sm *StateManager) GetAgentsReadyToWake() ([]string, error) {
	now := time.Now().Unix()
	query := sq.Select("agent_id").
		From("agent_states").
		Where(sq.Eq{"state": string(StateWaitingExternal)}).
		Where(sq.NotEq{"next_wake": nil}).
		Where(sq.LtOrEq{"next_wake": now})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := sm.db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query agents ready to wake: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var agentIDs []string
	for rows.Next() {
		var agentID string
		if err := rows.Scan(&agentID); err != nil {
			return nil, fmt.Errorf("failed to scan agent ID: %w", err)
		}
		agentIDs = append(agentIDs, agentID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agents ready to wake: %w", err)
	}

	return agentIDs, nil
}

// StatsManager manages agent statistics persistence
type StatsManager struct {
	db     *sql.DB
	logger zerolog.Logger
}

// NewStatsManager creates a new StatsManager
func NewStatsManager(logger zerolog.Logger, db *sql.DB) *StatsManager {
	return &StatsManager{db: db, logger: logger.With().Str("component", "statsManager").Logger()}
}

// IncrementExecutionCount increments the execution count and updates last_execution timestamp
func (sm *StatsManager) IncrementExecutionCount(agentID string) error {
	now := time.Now().Unix()
	query := sq.Insert("agent_stats").
		Columns("agent_id", "execution_count", "last_execution").
		Values(agentID, 1, now).
		Suffix("ON CONFLICT(agent_id) DO UPDATE SET execution_count = execution_count + 1, last_execution = excluded.last_execution")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = sm.db.Exec(queryStr, args...)
	if err != nil {
		return fmt.Errorf("failed to increment execution count: %w", err)
	}
	return nil
}

// IncrementFailureCount increments the failure count and updates last_failure timestamp and message
func (sm *StatsManager) IncrementFailureCount(agentID, errorMessage string) error {
	now := time.Now().Unix()
	query := sq.Insert("agent_stats").
		Columns("agent_id", "failure_count", "last_failure", "last_failure_message").
		Values(agentID, 1, now, errorMessage).
		Suffix("ON CONFLICT(agent_id) DO UPDATE SET failure_count = failure_count + 1, last_failure = excluded.last_failure, last_failure_message = excluded.last_failure_message")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = sm.db.Exec(queryStr, args...)
	if err != nil {
		return fmt.Errorf("failed to increment failure count: %w", err)
	}
	return nil
}

// IncrementWakeupCount increments the wakeup count
func (sm *StatsManager) IncrementWakeupCount(agentID string) error {
	query := sq.Insert("agent_stats").
		Columns("agent_id", "wakeup_count").
		Values(agentID, 1).
		Suffix("ON CONFLICT(agent_id) DO UPDATE SET wakeup_count = wakeup_count + 1")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return fmt.Errorf("build query: %w", err)
	}

	_, err = sm.db.Exec(queryStr, args...)
	if err != nil {
		return fmt.Errorf("failed to increment wakeup count: %w", err)
	}
	return nil
}

// GetStats retrieves stats for an agent
func (sm *StatsManager) GetStats(agentID string) (map[string]interface{}, error) {
	var executionCount, failureCount, wakeupCount int
	var lastExecution, lastFailure sql.NullInt64
	var lastFailureMessage sql.NullString

	query := sq.Select("execution_count", "failure_count", "wakeup_count", "last_execution", "last_failure", "last_failure_message").
		From("agent_stats").
		Where(sq.Eq{"agent_id": agentID})

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	err = sm.db.QueryRow(queryStr, args...).Scan(&executionCount, &failureCount, &wakeupCount, &lastExecution, &lastFailure, &lastFailureMessage)

	if err == sql.ErrNoRows {
		// Return zero stats if agent has no stats yet
		return map[string]interface{}{
			"agent_id":             agentID,
			"execution_count":      0,
			"failure_count":        0,
			"wakeup_count":         0,
			"last_execution":       nil,
			"last_failure":         nil,
			"last_failure_message": nil,
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get agent stats: %w", err)
	}

	result := map[string]interface{}{
		"agent_id":        agentID,
		"execution_count": executionCount,
		"failure_count":   failureCount,
		"wakeup_count":    wakeupCount,
	}

	if lastExecution.Valid {
		result["last_execution"] = lastExecution.Int64
	} else {
		result["last_execution"] = nil
	}

	if lastFailure.Valid {
		result["last_failure"] = lastFailure.Int64
	} else {
		result["last_failure"] = nil
	}

	if lastFailureMessage.Valid {
		result["last_failure_message"] = lastFailureMessage.String
	} else {
		result["last_failure_message"] = nil
	}

	return result, nil
}

// GetAllStats retrieves stats for all agents
func (sm *StatsManager) GetAllStats() ([]map[string]interface{}, error) {
	query := sq.Select("agent_id", "execution_count", "failure_count", "wakeup_count", "last_execution", "last_failure", "last_failure_message").
		From("agent_stats")

	queryStr, args, err := query.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	rows, err := sm.db.Query(queryStr, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent stats: %w", err)
	}
	defer rows.Close() //nolint:errcheck // No remedy for rows close errors

	var results []map[string]interface{}
	for rows.Next() {
		var agentID string
		var executionCount, failureCount, wakeupCount int
		var lastExecution, lastFailure sql.NullInt64
		var lastFailureMessage sql.NullString

		if err := rows.Scan(&agentID, &executionCount, &failureCount, &wakeupCount, &lastExecution, &lastFailure, &lastFailureMessage); err != nil {
			return nil, fmt.Errorf("failed to scan agent stats: %w", err)
		}

		result := map[string]interface{}{
			"agent_id":        agentID,
			"execution_count": executionCount,
			"failure_count":   failureCount,
			"wakeup_count":    wakeupCount,
		}

		if lastExecution.Valid {
			result["last_execution"] = lastExecution.Int64
		} else {
			result["last_execution"] = nil
		}

		if lastFailure.Valid {
			result["last_failure"] = lastFailure.Int64
		} else {
			result["last_failure"] = nil
		}

		if lastFailureMessage.Valid {
			result["last_failure_message"] = lastFailureMessage.String
		} else {
			result["last_failure_message"] = nil
		}

		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating agent stats: %w", err)
	}

	return results, nil
}
