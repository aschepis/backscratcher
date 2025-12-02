package server

import (
	"context"

	"github.com/samber/lo"
	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"github.com/aschepis/backscratcher/staff/llm"
	"github.com/aschepis/backscratcher/staff/ui"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Chat handles streaming chat with an agent.
func (s *Server) Chat(req *staffpb.ChatRequest, stream staffpb.ChatService_ChatServer) error {
	ctx := stream.Context()

	if req.AgentId == "" {
		return status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if req.ThreadId == "" {
		return status.Error(codes.InvalidArgument, "thread_id is required")
	}
	if req.Message == "" {
		return status.Error(codes.InvalidArgument, "message is required")
	}

	s.logger.Info().
		Str("agent_id", req.AgentId).
		Str("thread_id", req.ThreadId).
		Int("message_len", len(req.Message)).
		Msg("Chat request received")

	// Load conversation history from database
	history, err := s.chatService.LoadThread(ctx, req.AgentId, req.ThreadId)
	if err != nil {
		s.logger.Error().Err(err).Msg("Failed to load thread history")
		return status.Errorf(codes.Internal, "failed to load history: %v", err)
	}

	// Stream callback sends text deltas to the gRPC stream
	streamCallback := func(text string) error {
		return stream.Send(&staffpb.ChatEvent{
			Event: &staffpb.ChatEvent_TextDelta{
				TextDelta: &staffpb.TextDelta{Text: text},
			},
		})
	}

	// Execute the agent with streaming
	response, err := s.chatService.SendMessageStream(ctx, req.AgentId, req.ThreadId, req.Message, history, streamCallback)
	if err != nil {
		s.logger.Error().Err(err).Msg("Chat execution failed")
		// Send error event before returning
		_ = stream.Send(&staffpb.ChatEvent{
			Event: &staffpb.ChatEvent_Error{
				Error: &staffpb.ChatError{
					Message: err.Error(),
					Code:    "EXECUTION_ERROR",
				},
			},
		})
		return status.Errorf(codes.Internal, "chat execution failed: %v", err)
	}

	// Send completion event
	if err := stream.Send(&staffpb.ChatEvent{
		Event: &staffpb.ChatEvent_Complete{
			Complete: &staffpb.ChatComplete{
				FullResponse: response,
				StopReason:   "end_turn",
			},
		},
	}); err != nil {
		return status.Errorf(codes.Internal, "failed to send completion: %v", err)
	}

	s.logger.Info().
		Str("agent_id", req.AgentId).
		Str("thread_id", req.ThreadId).
		Int("response_len", len(response)).
		Msg("Chat completed")

	return nil
}

// GetOrCreateThread gets an existing thread or creates a new one.
func (s *Server) GetOrCreateThread(ctx context.Context, req *staffpb.GetThreadRequest) (*staffpb.GetThreadResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	threadID, err := s.chatService.GetOrCreateThreadID(ctx, req.AgentId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get/create thread: %v", err)
	}

	// TODO: Track whether this was a new thread or existing
	return &staffpb.GetThreadResponse{
		ThreadId: threadID,
		Created:  false, // We don't currently track this
	}, nil
}

// LoadHistory loads conversation history for a thread.
func (s *Server) LoadHistory(ctx context.Context, req *staffpb.LoadHistoryRequest) (*staffpb.LoadHistoryResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if req.ThreadId == "" {
		return nil, status.Error(codes.InvalidArgument, "thread_id is required")
	}

	var messages []llm.Message
	var err error

	if req.IncludeAll {
		// Load all messages for display purposes
		msgsWithTimestamp, err := s.chatService.LoadAllMessagesWithTimestamps(ctx, req.AgentId, req.ThreadId)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to load history: %v", err)
		}
		// Convert to response format
		pbMessages := lo.Map(msgsWithTimestamp, func(m ui.MessageWithTimestamp, _ int) *staffpb.Message {
			return convertMessageToProto(m.Message, m.Timestamp)
		})
		return &staffpb.LoadHistoryResponse{Messages: pbMessages}, nil
	}

	// Load active context messages (after last reset/compress)
	messages, err = s.chatService.LoadThread(ctx, req.AgentId, req.ThreadId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to load history: %v", err)
	}

	pbMessages := lo.Map(messages, func(m llm.Message, _ int) *staffpb.Message {
		return convertMessageToProto(m, 0)
	})

	return &staffpb.LoadHistoryResponse{Messages: pbMessages}, nil
}

// ResetContext clears the conversation context.
func (s *Server) ResetContext(ctx context.Context, req *staffpb.ContextRequest) (*staffpb.ContextResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if req.ThreadId == "" {
		return nil, status.Error(codes.InvalidArgument, "thread_id is required")
	}

	if err := s.chatService.ResetContext(ctx, req.AgentId, req.ThreadId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reset context: %v", err)
	}

	return &staffpb.ContextResponse{
		Success: true,
		Message: "Context reset successfully",
	}, nil
}

// CompressContext compresses the conversation context.
func (s *Server) CompressContext(ctx context.Context, req *staffpb.ContextRequest) (*staffpb.ContextResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if req.ThreadId == "" {
		return nil, status.Error(codes.InvalidArgument, "thread_id is required")
	}

	if err := s.chatService.CompressContext(ctx, req.AgentId, req.ThreadId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to compress context: %v", err)
	}

	return &staffpb.ContextResponse{
		Success: true,
		Message: "Context compressed successfully",
	}, nil
}

// convertMessageToProto converts an llm.Message to protobuf format.
func convertMessageToProto(m llm.Message, timestamp int64) *staffpb.Message {
	pb := &staffpb.Message{
		Role:      string(m.Role),
		Timestamp: timestamp,
	}

	// Handle content blocks
	if len(m.Content) > 0 {
		// For simplicity, concatenate text content
		var textContent string
		for _, block := range m.Content {
			switch block.Type {
			case llm.ContentBlockTypeText:
				textContent += block.Text
			case llm.ContentBlockTypeToolUse:
				if block.ToolUse != nil {
					pb.ToolId = block.ToolUse.ID
					pb.ToolName = block.ToolUse.Name
				}
			case llm.ContentBlockTypeToolResult:
				if block.ToolResult != nil {
					pb.ToolId = block.ToolResult.ID
					textContent = block.ToolResult.Content
				}
			}
		}
		pb.Content = textContent
	}

	return pb
}
