package client

import (
	"fmt"
	"strings"

	"github.com/aschepis/backscratcher/staff/api/staffpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	// DefaultSocketPath is the default Unix socket path for the daemon.
	DefaultSocketPath = "/tmp/staffd.sock"
)

// Client is the main client for interacting with the staffd daemon.
// It provides access to all gRPC service clients.
type Client struct {
	conn *grpc.ClientConn

	// Service clients
	Chat   staffpb.ChatServiceClient
	Agent  staffpb.AgentServiceClient
	Inbox  staffpb.InboxServiceClient
	Memory staffpb.MemoryServiceClient
	System staffpb.SystemServiceClient
}

// Connect connects to the staffd daemon.
// The address can be:
//   - A Unix socket path (e.g., "/tmp/staffd.sock")
//   - A TCP address (e.g., "localhost:50051")
//
// If the address starts with "unix://", it will be treated as a Unix socket.
// Otherwise, if it contains ":" it will be treated as TCP, else Unix socket.
func Connect(address string) (*Client, error) {
	var target string
	var opts []grpc.DialOption

	// Determine connection type
	switch {
	case strings.HasPrefix(address, "unix://"):
		// Explicit Unix socket
		target = address
	case strings.Contains(address, ":") && !strings.HasPrefix(address, "/"):
		// TCP address (contains : and doesn't start with /)
		target = address
	default:
		// Unix socket path
		target = "unix://" + address
	}
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))

	// Create gRPC connection
	conn, err := grpc.NewClient(target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to daemon at %s: %w", address, err)
	}

	// Create service clients
	client := &Client{
		conn:   conn,
		Chat:   staffpb.NewChatServiceClient(conn),
		Agent:  staffpb.NewAgentServiceClient(conn),
		Inbox:  staffpb.NewInboxServiceClient(conn),
		Memory: staffpb.NewMemoryServiceClient(conn),
		System: staffpb.NewSystemServiceClient(conn),
	}

	return client, nil
}

// Close closes the connection to the daemon.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}
