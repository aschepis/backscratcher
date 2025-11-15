package ollama

import (
	"context"
	"fmt"

	"github.com/aschepis/backscratcher/staff/memory"
	"github.com/ollama/ollama/api"
)

type Model string

const (
	ModelMXBAI Model = "mxbai-embed-large"
)

type embedder struct {
	client *api.Client
	model  Model
}

func NewEmbedder(model Model) (memory.Embedder, error) {
	cli, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, err
	}
	return &embedder{client: cli, model: model}, nil
}

func (e *embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := e.client.Embed(ctx, &api.EmbedRequest{
		Model: string(e.model),
		Input: text,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to embed text: %w", err)
	}
	return resp.Embeddings[0], nil
}
