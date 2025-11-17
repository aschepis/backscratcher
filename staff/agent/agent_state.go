package agent

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/aschepis/backscratcher/staff/logger"
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
	db *sql.DB
}

// NewStateManager creates a new StateManager
func NewStateManager(db *sql.DB) *StateManager {
	return &StateManager{db: db}
}

// StateExists checks if an agent has a persisted state
func (sm *StateManager) StateExists(agentID string) (bool, error) {
	var count int
	err := sm.db.QueryRow(
		`SELECT COUNT(*) FROM agent_states WHERE agent_id = ?`,
		agentID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check if agent state exists: %w", err)
	}
	return count > 0, nil
}

// GetState retrieves the current state of an agent
func (sm *StateManager) GetState(agentID string) (State, error) {
	var stateStr string
	var updatedAt int64
	err := sm.db.QueryRow(
		`SELECT state, updated_at FROM agent_states WHERE agent_id = ?`,
		agentID,
	).Scan(&stateStr, &updatedAt)

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

	_, err := sm.db.Exec(
		`INSERT INTO agent_states (agent_id, state, updated_at, next_wake)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   state = excluded.state,
		   updated_at = excluded.updated_at,
		   next_wake = excluded.next_wake`,
		agentID,
		string(state),
		now,
		nextWakeUnix,
	)
	if err != nil {
		logger.Error("Failed to set agent state: agentID=%s state=%s error=%v", agentID, state, err)
		return fmt.Errorf("failed to set agent state: %w", err)
	}

	logger.Info("Agent state updated: agentID=%s state=%s next_wake=%v", agentID, state, nextWakeUnix)
	return nil
}

// GetAllStates retrieves all agent states
func (sm *StateManager) GetAllStates() (map[string]State, error) {
	rows, err := sm.db.Query(`SELECT agent_id, state FROM agent_states`)
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
	rows, err := sm.db.Query(`SELECT agent_id FROM agent_states WHERE state = ?`, string(state))
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

	_, err := sm.db.Exec(
		`UPDATE agent_states SET next_wake = ? WHERE agent_id = ?`,
		nextWakeUnix,
		agentID,
	)
	if err != nil {
		logger.Error("Failed to set next wake: agentID=%s nextWake=%d error=%v", agentID, nextWakeUnix, err)
		return fmt.Errorf("failed to set next wake: %w", err)
	}

	logger.Info("Agent next wake updated: agentID=%s nextWake=%d", agentID, nextWakeUnix)
	return nil
}

// GetNextWake retrieves the next wake time for an agent
func (sm *StateManager) GetNextWake(agentID string) (*time.Time, error) {
	var nextWakeUnix sql.NullInt64
	err := sm.db.QueryRow(
		`SELECT next_wake FROM agent_states WHERE agent_id = ?`,
		agentID,
	).Scan(&nextWakeUnix)

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
	rows, err := sm.db.Query(
		`SELECT agent_id FROM agent_states 
		 WHERE state = ? AND next_wake IS NOT NULL AND next_wake <= ?`,
		string(StateWaitingExternal),
		now,
	)
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
	db *sql.DB
}

// NewStatsManager creates a new StatsManager
func NewStatsManager(db *sql.DB) *StatsManager {
	return &StatsManager{db: db}
}

// IncrementExecutionCount increments the execution count and updates last_execution timestamp
func (sm *StatsManager) IncrementExecutionCount(agentID string) error {
	now := time.Now().Unix()
	_, err := sm.db.Exec(
		`INSERT INTO agent_stats (agent_id, execution_count, last_execution)
		 VALUES (?, 1, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   execution_count = execution_count + 1,
		   last_execution = excluded.last_execution`,
		agentID,
		now,
	)
	if err != nil {
		return fmt.Errorf("failed to increment execution count: %w", err)
	}
	return nil
}

// IncrementFailureCount increments the failure count and updates last_failure timestamp and message
func (sm *StatsManager) IncrementFailureCount(agentID, errorMessage string) error {
	now := time.Now().Unix()
	_, err := sm.db.Exec(
		`INSERT INTO agent_stats (agent_id, failure_count, last_failure, last_failure_message)
		 VALUES (?, 1, ?, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   failure_count = failure_count + 1,
		   last_failure = excluded.last_failure,
		   last_failure_message = excluded.last_failure_message`,
		agentID,
		now,
		errorMessage,
	)
	if err != nil {
		return fmt.Errorf("failed to increment failure count: %w", err)
	}
	return nil
}

// IncrementWakeupCount increments the wakeup count
func (sm *StatsManager) IncrementWakeupCount(agentID string) error {
	_, err := sm.db.Exec(
		`INSERT INTO agent_stats (agent_id, wakeup_count)
		 VALUES (?, 1)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   wakeup_count = wakeup_count + 1`,
		agentID,
	)
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

	err := sm.db.QueryRow(
		`SELECT execution_count, failure_count, wakeup_count, last_execution, last_failure, last_failure_message
		 FROM agent_stats WHERE agent_id = ?`,
		agentID,
	).Scan(&executionCount, &failureCount, &wakeupCount, &lastExecution, &lastFailure, &lastFailureMessage)

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
	rows, err := sm.db.Query(
		`SELECT agent_id, execution_count, failure_count, wakeup_count, last_execution, last_failure, last_failure_message
		 FROM agent_stats`,
	)
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
