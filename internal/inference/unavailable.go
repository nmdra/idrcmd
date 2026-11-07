//go:build !needle || !cgo || !linux || !amd64

package inference

import (
	"context"
	"time"
)

func probe(context.Context, string) (ProbeResult, error) {
	return ProbeResult{}, ErrUnavailable
}

func openModel(context.Context, string) (*Model, LoadMetrics, error) {
	return nil, LoadMetrics{}, ErrUnavailable
}
func (*Model) embed(context.Context, string) ([]float32, time.Duration, error) {
	return nil, 0, ErrUnavailable
}
func (*Model) complete(context.Context, string, string) (string, time.Duration, error) {
	return "", 0, ErrUnavailable
}
