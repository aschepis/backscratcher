package memory

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
)

// Embedder is a pluggable interface for getting embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// EncodeEmbedding encodes a []float32 into a []byte for storage.
func EncodeEmbedding(vec []float32) []byte {
	if vec == nil {
		return nil
	}
	b := make([]byte, len(vec)*4)
	for i, f := range vec {
		u := math.Float32bits(f)
		binary.LittleEndian.PutUint32(b[i*4:], u)
	}
	return b
}

// DecodeEmbedding decodes a []byte into a []float32.
func DecodeEmbedding(b []byte) ([]float32, error) {
	if b == nil {
		return nil, nil
	}
	if len(b)%4 != 0 {
		return nil, errors.New("invalid embedding blob length")
	}
	vec := make([]float32, len(b)/4)
	for i := range vec {
		u := binary.LittleEndian.Uint32(b[i*4:])
		vec[i] = math.Float32frombits(u)
	}
	return vec, nil
}

// CosineSimilarity between two equal-length vectors.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		fa, fb := float64(a[i]), float64(b[i])
		dot += fa * fb
		na += fa * fa
		nb += fb * fb
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
