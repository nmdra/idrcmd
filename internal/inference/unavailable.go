//go:build !needle || !cgo || !linux || !amd64

package inference

import "context"

func probe(context.Context, string) (ProbeResult, error) {
	return ProbeResult{}, ErrUnavailable
}
