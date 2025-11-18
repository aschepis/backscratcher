package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/logger"
)

// Scheduler manages automatic waking of scheduled agents
type Scheduler struct {
	crew         *agent.Crew
	stateMgr     *agent.StateManager
	statsMgr     *agent.StatsManager
	pollInterval time.Duration
}

// NewScheduler creates a new scheduler with the given crew, state manager, stats manager, and poll interval
func NewScheduler(crew *agent.Crew, stateMgr *agent.StateManager, statsMgr *agent.StatsManager, pollInterval time.Duration) (*Scheduler, error) {
	if statsMgr == nil {
		return nil, fmt.Errorf("statsMgr cannot be nil")
	}
	return &Scheduler{
		crew:         crew,
		stateMgr:     stateMgr,
		statsMgr:     statsMgr,
		pollInterval: pollInterval,
	}, nil
}

// Start begins the scheduler goroutine that polls for agents ready to wake
func (s *Scheduler) Start(ctx context.Context) {
	logger.Info("Starting scheduler with poll interval: %v", s.pollInterval)

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Run initial check immediately
	logger.Info("Scheduler: performing initial check for agents ready to wake")
	s.checkAndWakeAgents(ctx)

	// Log that we're entering the polling loop
	logger.Info("Scheduler: entering polling loop (will check every %v)", s.pollInterval)

	for {
		select {
		case <-ctx.Done():
			logger.Info("Scheduler stopped: context cancelled")
			return
		case <-ticker.C:
			s.checkAndWakeAgents(ctx)
		}
	}
}

// checkAndWakeAgents checks for agents ready to wake and wakes them
func (s *Scheduler) checkAndWakeAgents(ctx context.Context) {
	// Get agents ready to wake
	agentIDs, err := s.stateMgr.GetAgentsReadyToWake()
	if err != nil {
		logger.Error("Failed to get agents ready to wake: %v", err)
		return
	}

	if len(agentIDs) == 0 {
		return
	}

	logger.Info("Found %d agent(s) ready to wake", len(agentIDs))

	// Wake each agent
	for _, agentID := range agentIDs {
		// Skip disabled agents
		if s.crew.IsAgentDisabled(agentID) {
			logger.Debug("Scheduler: skipping disabled agent %s", agentID)
			continue
		}
		s.wakeAgent(ctx, agentID)
	}
}

// wakeAgent wakes a single agent by calling Crew.Run with "continue" message
func (s *Scheduler) wakeAgent(ctx context.Context, agentID string) {
	logger.Info("Waking agent: %s", agentID)

	// Track wakeup
	if err := s.statsMgr.IncrementWakeupCount(agentID); err != nil {
		logger.Warn("Failed to update wakeup stats for agent %s: %v", agentID, err)
	}

	// Create a new context with timeout for the agent run
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Call Crew.Run with "continue" message and empty history
	_, err := s.crew.Run(runCtx, agentID, fmt.Sprintf("scheduled-%d", time.Now().Unix()), "continue", nil)
	if err != nil {
		logger.Error("Failed to wake agent %s: %v", agentID, err)
		return
	}

	logger.Info("Successfully woke agent: %s", agentID)
}
