package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/rs/zerolog"
)

// Scheduler manages automatic waking of scheduled agents
type Scheduler struct {
	crew         *agent.Crew
	stateMgr     *agent.StateManager
	statsMgr     *agent.StatsManager
	pollInterval time.Duration
	logger       zerolog.Logger
}

// NewScheduler creates a new scheduler with the given crew, state manager, stats manager, and poll interval
func NewScheduler(crew *agent.Crew, stateMgr *agent.StateManager, statsMgr *agent.StatsManager, pollInterval time.Duration, logger zerolog.Logger) (*Scheduler, error) {
	if statsMgr == nil {
		return nil, fmt.Errorf("statsMgr cannot be nil")
	}
	return &Scheduler{
		crew:         crew,
		stateMgr:     stateMgr,
		statsMgr:     statsMgr,
		pollInterval: pollInterval,
		logger:       logger.With().Str("component", "scheduler").Logger(),
	}, nil
}

// Start begins the scheduler goroutine that polls for agents ready to wake
func (s *Scheduler) Start(ctx context.Context) {
	s.logger.Info().Dur("pollInterval", s.pollInterval).Msg("Starting scheduler")

	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()

	// Run initial check immediately
	s.logger.Info().Msg("Scheduler: performing initial check for agents ready to wake")
	s.checkAndWakeAgents(ctx)

	// Log that we're entering the polling loop
	s.logger.Info().Dur("pollInterval", s.pollInterval).Msg("Scheduler: entering polling loop")

	for {
		select {
		case <-ctx.Done():
			s.logger.Info().Msg("Scheduler stopped: context cancelled")
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
		s.logger.Error().Err(err).Msg("Failed to get agents ready to wake")
		return
	}

	if len(agentIDs) == 0 {
		return
	}

	s.logger.Info().Int("numAgents", len(agentIDs)).Msg("Found agents ready to wake")

	// Wake each agent
	for _, agentID := range agentIDs {
		// Skip disabled agents
		if s.crew.IsAgentDisabled(agentID) {
			s.logger.Debug().Str("agentID", agentID).Msg("Scheduler: skipping disabled agent")
			continue
		}
		s.wakeAgent(ctx, agentID)
	}
}

// wakeAgent wakes a single agent by calling Crew.Run with "continue" message
func (s *Scheduler) wakeAgent(ctx context.Context, agentID string) {
	s.logger.Info().Str("agentID", agentID).Msg("Waking agent")

	// Track wakeup
	if err := s.statsMgr.IncrementWakeupCount(agentID); err != nil {
		s.logger.Warn().Str("agentID", agentID).Err(err).Msg("Failed to update wakeup stats")
	}

	// Create a new context with timeout for the agent run
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	// Call Crew.Run with "continue" message and empty history
	_, err := s.crew.Run(runCtx, agentID, fmt.Sprintf("scheduled-%d", time.Now().Unix()), "continue", nil)
	if err != nil {
		s.logger.Error().Err(err).Str("agentID", agentID).Msg("Failed to wake agent")
		return
	}

	s.logger.Info().Str("agentID", agentID).Msg("Successfully woke agent")
}
