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
	StateIdle           State = "idle"
	StateRunning        State = "running"
	StateWaitingHuman   State = "waiting_human"
	StateWaitingExternal State = "waiting_external"
	StateSleeping       State = "sleeping"
)

// AgentState represents the state of an agent in the database
type AgentState struct {
	AgentID  string
	State    State
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
	
	_, err := sm.db.Exec(
		`INSERT INTO agent_states (agent_id, state, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(agent_id) DO UPDATE SET
		   state = excluded.state,
		   updated_at = excluded.updated_at`,
		agentID,
		string(state),
		now,
	)
	if err != nil {
		logger.Error("Failed to set agent state: agentID=%s state=%s error=%v", agentID, state, err)
		return fmt.Errorf("failed to set agent state: %w", err)
	}
	
	logger.Info("Agent state updated: agentID=%s state=%s", agentID, state)
	return nil
}

// GetAllStates retrieves all agent states
func (sm *StateManager) GetAllStates() (map[string]State, error) {
	rows, err := sm.db.Query(`SELECT agent_id, state FROM agent_states`)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent states: %w", err)
	}
	defer rows.Close()
	
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
	defer rows.Close()
	
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

