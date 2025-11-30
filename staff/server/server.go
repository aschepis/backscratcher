// Package server implements the gRPC server for staffd daemon.
package server

import (
	"context"
	"database/sql"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/aschepis/backscratcher/staff/agent"
	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/aschepis/backscratcher/staff/ui"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// Server is the main gRPC server for staffd.
type Server struct {
	staffpb.UnimplementedChatServiceServer
	staffpb.UnimplementedAgentServiceServer
	staffpb.UnimplementedInboxServiceServer
	staffpb.UnimplementedMemoryServiceServer
	staffpb.UnimplementedSystemServiceServer

	grpcServer   *grpc.Server
	crew         *agent.Crew
	db           *sql.DB
	memoryRouter *memory.MemoryRouter
	memoryStore  *memory.Store
	chatService  ui.ChatService
	logger       zerolog.Logger

	// Server state
	startedAt  time.Time
	socketPath string

	// Client tracking for connected clients count
	clientsMu sync.RWMutex
	clients   map[string]struct{}

	// State change notifications for WatchStates
	stateWatchersMu sync.RWMutex
	stateWatchers   map[chan *staffpb.AgentState][]string // chan -> agent_ids filter

	// Inbox notifications for Watch
	inboxWatchersMu sync.RWMutex
	inboxWatchers   map[chan *staffpb.InboxItem]struct{}
}

// Config holds server configuration options.
type Config struct {
	SocketPath string
	Logger     zerolog.Logger
}

// New creates a new gRPC server.
func New(cfg Config, crew *agent.Crew, db *sql.DB, memoryRouter *memory.MemoryRouter, memoryStore *memory.Store, chatService ui.ChatService) *Server {
	s := &Server{
		crew:          crew,
		db:            db,
		memoryRouter:  memoryRouter,
		memoryStore:   memoryStore,
		chatService:   chatService,
		logger:        cfg.Logger.With().Str("component", "grpc-server").Logger(),
		socketPath:    cfg.SocketPath,
		clients:       make(map[string]struct{}),
		stateWatchers: make(map[chan *staffpb.AgentState][]string),
		inboxWatchers: make(map[chan *staffpb.InboxItem]struct{}),
	}

	// Create gRPC server with interceptors
	s.grpcServer = grpc.NewServer(
		grpc.ChainUnaryInterceptor(s.loggingInterceptor),
		grpc.ChainStreamInterceptor(s.streamLoggingInterceptor),
	)

	// Register all services
	staffpb.RegisterChatServiceServer(s.grpcServer, s)
	staffpb.RegisterAgentServiceServer(s.grpcServer, s)
	staffpb.RegisterInboxServiceServer(s.grpcServer, s)
	staffpb.RegisterMemoryServiceServer(s.grpcServer, s)
	staffpb.RegisterSystemServiceServer(s.grpcServer, s)

	// Enable reflection for debugging tools like grpcurl
	reflection.Register(s.grpcServer)

	return s
}

// Serve starts the gRPC server on the given listener.
func (s *Server) Serve(listener net.Listener) error {
	s.startedAt = time.Now()
	s.logger.Info().Str("address", listener.Addr().String()).Msg("Starting gRPC server")
	return s.grpcServer.Serve(listener)
}

// ServeUnix starts the server on a Unix domain socket.
func (s *Server) ServeUnix(socketPath string) error {
	// Remove existing socket file if it exists
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return err
	}
	s.socketPath = socketPath
	return s.Serve(listener)
}

// ServeTCP starts the server on a TCP address.
func (s *Server) ServeTCP(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

// GracefulStop gracefully stops the server.
func (s *Server) GracefulStop() {
	s.logger.Info().Msg("Gracefully stopping gRPC server")
	s.grpcServer.GracefulStop()
}

// Stop immediately stops the server.
func (s *Server) Stop() {
	s.logger.Info().Msg("Stopping gRPC server")
	s.grpcServer.Stop()
}

// loggingInterceptor logs unary RPC calls.
func (s *Server) loggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)

	if err != nil {
		s.logger.Error().
			Str("method", info.FullMethod).
			Dur("duration", duration).
			Err(err).
			Msg("RPC failed")
	} else {
		s.logger.Debug().
			Str("method", info.FullMethod).
			Dur("duration", duration).
			Msg("RPC completed")
	}

	return resp, err
}

// streamLoggingInterceptor logs streaming RPC calls.
func (s *Server) streamLoggingInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	s.logger.Debug().
		Str("method", info.FullMethod).
		Bool("client_stream", info.IsClientStream).
		Bool("server_stream", info.IsServerStream).
		Msg("Stream started")

	err := handler(srv, ss)
	duration := time.Since(start)

	if err != nil {
		s.logger.Error().
			Str("method", info.FullMethod).
			Dur("duration", duration).
			Err(err).
			Msg("Stream failed")
	} else {
		s.logger.Debug().
			Str("method", info.FullMethod).
			Dur("duration", duration).
			Msg("Stream completed")
	}

	return err
}

// subscribeStateChanges registers a channel to receive agent state changes.
func (s *Server) subscribeStateChanges(agentIDs []string) chan *staffpb.AgentState {
	ch := make(chan *staffpb.AgentState, 100)
	s.stateWatchersMu.Lock()
	s.stateWatchers[ch] = agentIDs
	s.stateWatchersMu.Unlock()
	return ch
}

// unsubscribeStateChanges removes a state change subscription.
func (s *Server) unsubscribeStateChanges(ch chan *staffpb.AgentState) {
	s.stateWatchersMu.Lock()
	delete(s.stateWatchers, ch)
	s.stateWatchersMu.Unlock()
	close(ch)
}

// broadcastStateChange sends a state change to all watchers.
func (s *Server) broadcastStateChange(state *staffpb.AgentState) {
	s.stateWatchersMu.RLock()
	defer s.stateWatchersMu.RUnlock()

	for ch, agentFilter := range s.stateWatchers {
		// If filter is empty, send all; otherwise check if agent is in filter
		if len(agentFilter) == 0 || slices.Contains(agentFilter, state.AgentId) {
			select {
			case ch <- state:
			default:
				// Channel full, skip (non-blocking)
			}
		}
	}
}

// subscribeInbox registers a channel to receive inbox notifications.
func (s *Server) subscribeInbox() chan *staffpb.InboxItem {
	ch := make(chan *staffpb.InboxItem, 100)
	s.inboxWatchersMu.Lock()
	s.inboxWatchers[ch] = struct{}{}
	s.inboxWatchersMu.Unlock()
	return ch
}

// unsubscribeInbox removes an inbox subscription.
func (s *Server) unsubscribeInbox(ch chan *staffpb.InboxItem) {
	s.inboxWatchersMu.Lock()
	delete(s.inboxWatchers, ch)
	s.inboxWatchersMu.Unlock()
	close(ch)
}

// broadcastInboxItem sends an inbox item to all watchers.
func (s *Server) broadcastInboxItem(item *staffpb.InboxItem) {
	s.inboxWatchersMu.RLock()
	defer s.inboxWatchersMu.RUnlock()

	for ch := range s.inboxWatchers {
		select {
		case ch <- item:
		default:
			// Channel full, skip (non-blocking)
		}
	}
}

// getConnectedClientCount returns the number of connected clients.
func (s *Server) getConnectedClientCount() int {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	return len(s.clients)
}
