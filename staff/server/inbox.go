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

// ListItems returns inbox items.
func (s *Server) ListItems(ctx context.Context, req *staffpb.ListInboxRequest) (*staffpb.ListInboxResponse, error) {
	items, err := s.chatService.ListInboxItems(ctx, req.IncludeArchived)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list inbox items: %v", err)
	}

	pbItems := lo.Map(items, func(item *ui.InboxItem, _ int) *staffpb.InboxItem {
		return convertInboxItemToProto(item)
	})

	return &staffpb.ListInboxResponse{Items: pbItems}, nil
}

// Archive marks an inbox item as archived.
func (s *Server) Archive(ctx context.Context, req *staffpb.ArchiveRequest) (*staffpb.ArchiveResponse, error) {
	if req.InboxId == 0 {
		return nil, status.Error(codes.InvalidArgument, "inbox_id is required")
	}

	if err := s.chatService.ArchiveInboxItem(ctx, req.InboxId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to archive item: %v", err)
	}

	return &staffpb.ArchiveResponse{Success: true}, nil
}

// Watch streams new inbox items as they arrive.
func (s *Server) Watch(req *staffpb.WatchInboxRequest, stream staffpb.InboxService_WatchServer) error {
	// Subscribe to inbox notifications
	ch := s.subscribeInbox()
	defer s.unsubscribeInbox(ch)

	s.logger.Info().Msg("Client subscribed to inbox notifications")

	// Stream items until client disconnects
	for {
		select {
		case <-stream.Context().Done():
			s.logger.Info().Msg("Client disconnected from inbox watch")
			return stream.Context().Err()
		case item, ok := <-ch:
			if !ok {
				return nil
			}
			if err := stream.Send(item); err != nil {
				return err
			}
		}
	}
}

// convertInboxItemToProto converts a ui.InboxItem to protobuf format.
func convertInboxItemToProto(item *ui.InboxItem) *staffpb.InboxItem {
	pb := &staffpb.InboxItem{
		Id:               item.ID,
		AgentId:          item.AgentID,
		ThreadId:         item.ThreadID,
		Message:          item.Message,
		RequiresResponse: item.RequiresResponse,
		Response:         item.Response,
		CreatedAt:        timestamppb.New(item.CreatedAt),
		UpdatedAt:        timestamppb.New(item.UpdatedAt),
	}

	if item.ResponseAt != nil {
		pb.ResponseAt = timestamppb.New(*item.ResponseAt)
	}
	if item.ArchivedAt != nil {
		pb.ArchivedAt = timestamppb.New(*item.ArchivedAt)
	}

	return pb
}
