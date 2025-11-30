package server

import (
	"context"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"github.com/samber/lo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ListAgents returns all configured agents.
func (s *Server) ListAgents(ctx context.Context, req *staffpb.ListAgentsRequest) (*staffpb.ListAgentsResponse, error) {
	// Get agent infos from crew (authoritative source)
	agentInfos := s.crew.GetAgentInfos()

	agents := lo.Map(agentInfos, func(info *agent.AgentInfo, _ int) *staffpb.Agent {
		return agentInfoToProto(info)
	})

	return &staffpb.ListAgentsResponse{Agents: agents}, nil
}

// GetAgent returns detailed information about a specific agent.
func (s *Server) GetAgent(ctx context.Context, req *staffpb.GetAgentRequest) (*staffpb.Agent, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	info, err := s.crew.GetAgentInfo(req.AgentId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "agent %q not found: %v", req.AgentId, err)
	}

	return agentInfoToProto(info), nil
}

// agentInfoToProto converts an agent.AgentInfo to protobuf format.
func agentInfoToProto(info *agent.AgentInfo) *staffpb.Agent {
	return &staffpb.Agent{
		Id:           info.ID,
		Name:         info.Name,
		Model:        info.Model,
		Provider:     info.Provider,
		Tools:        info.Tools,
		Schedule:     info.Schedule,
		Disabled:     info.Disabled,
		SystemPrompt: info.SystemPrompt,
		MaxTokens:    info.MaxTokens,
	}
}

// GetAgentState returns the current state of an agent.
func (s *Server) GetAgentState(ctx context.Context, req *staffpb.GetAgentStateRequest) (*staffpb.AgentState, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	state, err := s.crew.StateManager.GetState(req.AgentId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get state: %v", err)
	}

	nextWake, err := s.crew.StateManager.GetNextWake(req.AgentId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get next wake: %v", err)
	}

	result := &staffpb.AgentState{
		AgentId: req.AgentId,
		State:   string(state),
	}

	if nextWake != nil {
		result.NextWake = timestamppb.New(*nextWake)
	}

	return result, nil
}

// GetAgentStats returns statistics for an agent.
func (s *Server) GetAgentStats(ctx context.Context, req *staffpb.GetAgentStatsRequest) (*staffpb.AgentStats, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	stats, err := s.crew.StatsManager.GetStats(req.AgentId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get stats: %v", err)
	}

	result := &staffpb.AgentStats{
		AgentId: req.AgentId,
	}

	// Extract stats from the map
	if v, ok := stats["execution_count"].(int64); ok {
		result.ExecutionCount = v
	}
	if v, ok := stats["failure_count"].(int64); ok {
		result.FailureCount = v
	}
	if v, ok := stats["wakeup_count"].(int64); ok {
		result.WakeupCount = v
	}
	if v, ok := stats["last_failure_message"].(string); ok {
		result.LastFailureMessage = v
	}
	// TODO: Convert timestamps if present

	return result, nil
}

// WatchStates streams agent state changes.
func (s *Server) WatchStates(req *staffpb.WatchStatesRequest, stream staffpb.AgentService_WatchStatesServer) error {
	// Subscribe to state changes
	ch := s.subscribeStateChanges(req.AgentIds)
	defer s.unsubscribeStateChanges(ch)

	s.logger.Info().
		Strs("agent_ids", req.AgentIds).
		Msg("Client subscribed to state changes")

	// Send initial states for requested agents
	agentIDs := req.AgentIds
	if len(agentIDs) == 0 {
		// Get all agent IDs
		configs := s.crew.GetAgents()
		for id := range configs {
			agentIDs = append(agentIDs, id)
		}
	}

	for _, agentID := range agentIDs {
		state, err := s.crew.StateManager.GetState(agentID)
		if err != nil {
			continue
		}
		nextWake, _ := s.crew.StateManager.GetNextWake(agentID)

		initialState := &staffpb.AgentState{
			AgentId: agentID,
			State:   string(state),
		}
		if nextWake != nil {
			initialState.NextWake = timestamppb.New(*nextWake)
		}

		if err := stream.Send(initialState); err != nil {
			return err
		}
	}

	// Stream state changes until client disconnects
	for {
		select {
		case <-stream.Context().Done():
			s.logger.Info().Msg("Client disconnected from state watch")
			return stream.Context().Err()
		case stateChange, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(stateChange); err != nil {
				return err
			}
		}
	}
}
