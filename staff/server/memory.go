package server

import (
	"context"

	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/samber/lo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Search searches memories using the memory router.
func (s *Server) Search(ctx context.Context, req *staffpb.SearchMemoryRequest) (*staffpb.SearchMemoryResponse, error) {
	if req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	// Build search query
	query := &memory.SearchQuery{
		QueryText:     req.Query,
		Limit:         int(req.Limit),
		IncludeGlobal: req.Scope == "global",
	}

	if req.Scope == "agent" {
		if req.AgentId == "" {
			return nil, status.Error(codes.InvalidArgument, "agent_id is required when scope is 'agent'")
		}
		query.AgentID = &req.AgentId
	}

	// Convert types
	if len(req.Types) > 0 {
		query.Types = lo.Map(req.Types, func(t string, _ int) memory.MemoryType {
			return memory.MemoryType(t)
		})
	}

	// Execute search
	results, err := s.memoryStore.SearchMemory(ctx, query)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to search memory: %v", err)
	}

	// Convert results to protobuf
	pbItems := lo.Map(results, func(result memory.SearchResult, _ int) *staffpb.MemoryItem {
		return convertMemoryItemToProto(result.Item)
	})

	return &staffpb.SearchMemoryResponse{Items: pbItems}, nil
}

// Store stores a memory item.
func (s *Server) Store(ctx context.Context, req *staffpb.StoreMemoryRequest) (*staffpb.StoreMemoryResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}
	if req.Type == "" {
		return nil, status.Error(codes.InvalidArgument, "type is required")
	}
	if req.Content == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}

	// Convert metadata from structpb.Struct to map[string]interface{}
	var metadata map[string]interface{}
	if req.Metadata != nil {
		metadata = req.Metadata.AsMap()
	}

	var item memory.MemoryItem
	var err error

	// Route based on scope and type
	if req.Scope == "global" {
		// Store as global fact
		item, err = s.memoryRouter.AddGlobalFact(ctx, req.Content, metadata)
	} else {
		// Store as agent-specific memory
		importance := req.Importance
		if importance == 0 {
			importance = 0.5 // Default importance
		}

		// Determine memory type
		memoryType := req.Type
		if memoryType == "" {
			memoryType = "fact"
		}

		item, err = s.memoryRouter.StorePersonalMemory(
			ctx,
			req.AgentId,
			req.Content, // rawText
			req.Content, // normalized (same for now)
			memoryType,
			nil, // tags
			nil, // threadID
			importance,
			metadata,
		)
	}

	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to store memory: %v", err)
	}

	return &staffpb.StoreMemoryResponse{Id: item.ID}, nil
}

// Dump writes all memory items to a file.
func (s *Server) Dump(ctx context.Context, req *staffpb.DumpMemoryRequest) (*staffpb.DumpMemoryResponse, error) {
	if req.FilePath == "" {
		return nil, status.Error(codes.InvalidArgument, "file_path is required")
	}

	if err := s.chatService.DumpMemory(ctx, req.FilePath); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to dump memory: %v", err)
	}

	return &staffpb.DumpMemoryResponse{
		Success: true,
		Message: "Memory dumped successfully",
	}, nil
}

// Clear deletes all memory items.
func (s *Server) Clear(ctx context.Context, req *staffpb.ClearMemoryRequest) (*staffpb.ClearMemoryResponse, error) {
	if err := s.chatService.ClearMemory(ctx); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to clear memory: %v", err)
	}

	return &staffpb.ClearMemoryResponse{
		Success:      true,
		ItemsDeleted: 0, // TODO: return actual count
	}, nil
}

// convertMemoryItemToProto converts a memory.MemoryItem to protobuf format.
func convertMemoryItemToProto(item *memory.MemoryItem) *staffpb.MemoryItem {
	pb := &staffpb.MemoryItem{
		Id:         item.ID,
		Scope:      string(item.Scope),
		Type:       string(item.Type),
		Content:    item.Content,
		Importance: item.Importance,
		CreatedAt:  timestamppb.New(item.CreatedAt),
	}

	// Handle optional AgentID pointer
	if item.AgentID != nil {
		pb.AgentId = *item.AgentID
	}

	// Convert metadata to structpb.Struct
	if item.Metadata != nil {
		if metadataStruct, err := structpb.NewStruct(item.Metadata); err == nil {
			pb.Metadata = metadataStruct
		}
	}

	return pb
}
