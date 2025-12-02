package server

import (
	"context"

	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"github.com/aschepis/backscratcher/staff/ui"
	"github.com/samber/lo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetInfo returns daemon status and version information.
func (s *Server) GetInfo(ctx context.Context, req *staffpb.GetInfoRequest) (*staffpb.SystemInfo, error) {
	agents := s.crew.ListAgents()
	activeCount := 0
	for _, agent := range agents {
		if !agent.Config.Disabled {
			activeCount++
		}
	}

	return &staffpb.SystemInfo{
		Version:          "0.1.0", // TODO: get from build info
		Status:           "running",
		StartedAt:        timestamppb.New(s.startedAt),
		ActiveAgents:     int64(activeCount),
		ConnectedClients: int64(s.getConnectedClientCount()),
		SocketPath:       s.socketPath,
	}, nil
}

// ListTools returns all registered tools.
func (s *Server) ListTools(ctx context.Context, req *staffpb.ListToolsRequest) (*staffpb.ListToolsResponse, error) {
	tools, err := s.chatService.ListAllTools(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list tools: %v", err)
	}

	// Get system info for tool descriptions
	systemInfo, err := s.chatService.GetSystemInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get system info: %v", err)
	}

	// Build tool info map from system info
	toolMap := make(map[string]*staffpb.ToolInfo)
	for _, tool := range systemInfo.Tools {
		toolMap[tool.Name] = &staffpb.ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			Server:      tool.Server,
		}
	}

	// Convert tool names to ToolInfo
	pbTools := lo.Map(tools, func(toolName string, _ int) *staffpb.ToolInfo {
		// Parse tool name (may be "server:tool" format for MCP tools)
		var name, server string
		if idx := findColon(toolName); idx >= 0 {
			server = toolName[:idx]
			name = toolName[idx+1:]
		} else {
			name = toolName
		}

		// Get description if available
		description := ""
		if info, ok := toolMap[name]; ok {
			description = info.Description
		}

		return &staffpb.ToolInfo{
			Name:        name,
			Description: description,
			Server:      server,
		}
	})

	return &staffpb.ListToolsResponse{Tools: pbTools}, nil
}

// ListMCPServers returns information about MCP servers.
func (s *Server) ListMCPServers(ctx context.Context, req *staffpb.ListMCPServersRequest) (*staffpb.ListMCPServersResponse, error) {
	systemInfo, err := s.chatService.GetSystemInfo(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get system info: %v", err)
	}

	mcpServers := s.crew.GetMCPServers()
	mcpClients := s.crew.GetMCPClients()

	pbServers := lo.Map(systemInfo.MCPServers, func(serverInfo ui.MCPServerInfo, _ int) *staffpb.MCPServerInfo {
		// Get transport type from config
		transport := "unknown"
		if cfg, ok := mcpServers[serverInfo.Name]; ok {
			if cfg.Command != "" {
				transport = "stdio"
			} else if cfg.URL != "" {
				transport = "http"
			}
		}

		// Check if client is connected
		status := "disconnected"
		if _, ok := mcpClients[serverInfo.Name]; ok {
			status = "connected"
		}

		return &staffpb.MCPServerInfo{
			Name:      serverInfo.Name,
			Transport: transport,
			Status:    status,
			ToolCount: int64(len(serverInfo.Tools)),
			Tools:     serverInfo.Tools,
		}
	})

	return &staffpb.ListMCPServersResponse{Servers: pbServers}, nil
}

// DumpToolSchemas writes all tool schemas to a file.
func (s *Server) DumpToolSchemas(ctx context.Context, req *staffpb.DumpToolSchemasRequest) (*staffpb.DumpToolSchemasResponse, error) {
	if req.FilePath == "" {
		return nil, status.Error(codes.InvalidArgument, "file_path is required")
	}

	if err := s.chatService.DumpToolSchemas(ctx, req.FilePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump tool schemas: %v", err)
	}

	return &staffpb.DumpToolSchemasResponse{
		Success: true,
		Message: "Tool schemas dumped successfully",
	}, nil
}

// DumpConversations writes conversations to files.
func (s *Server) DumpConversations(ctx context.Context, req *staffpb.DumpConversationsRequest) (*staffpb.DumpConversationsResponse, error) {
	if req.OutputDir == "" {
		return nil, status.Error(codes.InvalidArgument, "output_dir is required")
	}

	if err := s.chatService.DumpConversations(ctx, req.OutputDir); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump conversations: %v", err)
	}

	return &staffpb.DumpConversationsResponse{
		Success: true,
		Message: "Conversations dumped successfully",
	}, nil
}

// ClearConversations deletes all conversations.
func (s *Server) ClearConversations(ctx context.Context, req *staffpb.ClearConversationsRequest) (*staffpb.ClearConversationsResponse, error) {
	if err := s.chatService.ClearConversations(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to clear conversations: %v", err)
	}

	return &staffpb.ClearConversationsResponse{Success: true}, nil
}

// ResetStats resets all agent statistics.
func (s *Server) ResetStats(ctx context.Context, req *staffpb.ResetStatsRequest) (*staffpb.ResetStatsResponse, error) {
	if err := s.chatService.ResetStats(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reset stats: %v", err)
	}

	return &staffpb.ResetStatsResponse{Success: true}, nil
}

// DumpInbox writes all inbox items to a file.
func (s *Server) DumpInbox(ctx context.Context, req *staffpb.DumpInboxRequest) (*staffpb.DumpInboxResponse, error) {
	if req.FilePath == "" {
		return nil, status.Error(codes.InvalidArgument, "file_path is required")
	}

	if err := s.chatService.DumpInbox(ctx, req.FilePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump inbox: %v", err)
	}

	return &staffpb.DumpInboxResponse{
		Success: true,
		Message: "Inbox dumped successfully",
	}, nil
}

// ClearInbox deletes all inbox items.
func (s *Server) ClearInbox(ctx context.Context, req *staffpb.ClearInboxRequest) (*staffpb.ClearInboxResponse, error) {
	if err := s.chatService.ClearInbox(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to clear inbox: %v", err)
	}

	return &staffpb.ClearInboxResponse{Success: true}, nil
}

// helper function to find colon in string
func findColon(s string) int {
	for i, r := range s {
		if r == ':' {
			return i
		}
	}
	return -1
}
