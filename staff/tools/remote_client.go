package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPRemoteCaller is a simple RemoteCaller implementation that sends tool
// calls over HTTP as JSON. This is not the official MCP protocol, but it
// demonstrates how an in-process tool handler can delegate to a remote
// process.
//
// Expected server contract (you can adapt as needed):
//
//	POST {BaseURL}/tools/{toolName}
//	Body:  { "args": { ... } }
//	Response: arbitrary JSON, returned as-is to the model.
type HTTPRemoteCaller struct {
	BaseURL    string
	HTTPClient *http.Client
	AuthToken  string
}

func NewHTTPRemoteCaller(baseURL string) *HTTPRemoteCaller {
	return &HTTPRemoteCaller{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (c *HTTPRemoteCaller) Call(ctx context.Context, toolName string, args json.RawMessage) (json.RawMessage, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("HTTPRemoteCaller: BaseURL is empty")
	}

	payload := map[string]json.RawMessage{
		"args": args,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/tools/%s", c.BaseURL, toolName)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // Body close error can be ignored

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTPRemoteCaller: remote error %s", resp.Status)
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(respBytes), nil
}
