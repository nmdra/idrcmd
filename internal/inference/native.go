//go:build needle && cgo && linux && amd64

package inference

/*
#cgo CFLAGS: -I${SRCDIR}/../../.native/include
#cgo LDFLAGS: ${SRCDIR}/../../.native/lib/libneedle.a -lc++ -lstdc++ -lm -lpthread
#include <stdlib.h>
#include "needle.h"
*/
import "C"

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

// This is the raw tool schema described by the model card's linked tool guide,
// not an executable template. There are no model-controlled paths or arguments.
const probeTools = `[{"name":"git_status","description":"Show uncommitted changes in the current Git repository (read only)","parameters":{"type":"object","properties":{},"required":[]}}]`
const probeIntent = "Show my uncommitted changes"

// The upstream header explicitly declares a process-global, non-thread-safe
// model and provides no unload function. Keep its C-owned weights alive for the
// process lifetime. No Go pointer is retained by C.
var nativeGate = make(chan struct{}, 1)
var loadedWeights unsafe.Pointer
var loadFailure error

func probe(ctx context.Context, modelPath string) (ProbeResult, error) {
	select {
	case nativeGate <- struct{}{}:
	case <-ctx.Done():
		return ProbeResult{}, ctx.Err()
	}
	defer func() { <-nativeGate }()
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}

	// Verify even when already loaded, so an invalid requested path never succeeds
	// because a previous probe happened to initialize the process-global model.
	fd, err := syscall.Open(modelPath, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("open Needle weights: %w", err)
	}
	f := os.NewFile(uintptr(fd), modelPath)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return ProbeResult{}, fmt.Errorf("inspect Needle weights: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != modelSize {
		f.Close()
		return ProbeResult{}, fmt.Errorf("Needle weights must be a regular file of pinned size")
	}
	data, err := io.ReadAll(io.LimitReader(f, modelSize+1))
	closeErr := f.Close()
	if err != nil {
		return ProbeResult{}, fmt.Errorf("read Needle weights: %w", err)
	}
	if closeErr != nil {
		return ProbeResult{}, closeErr
	}
	if len(data) != modelSize || fmt.Sprintf("%x", sha256.Sum256(data)) != modelSHA256 {
		return ProbeResult{}, fmt.Errorf("Needle weights do not match pinned size and SHA256")
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	if loadFailure != nil {
		return ProbeResult{}, loadFailure
	}
	if loadedWeights == nil {
		p := C.CBytes(data)
		if p == nil {
			return ProbeResult{}, fmt.Errorf("allocate Needle weights")
		}
		rc := C.needle_load((*C.uchar)(p), C.ulonglong(len(data)))
		// The ABI does not document failure-path ownership either. Retain the buffer
		// even on a native load failure rather than risk use-after-free.
		loadedWeights = p
		if rc < 0 {
			loadFailure = fmt.Errorf("needle_load failed: %d (restart process before retry)", int(rc))
			return ProbeResult{}, loadFailure
		}
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	system := C.CString("You select only the provided read-only tool. Never execute commands.")
	tools := C.CString(probeTools)
	defer C.free(unsafe.Pointer(system))
	defer C.free(unsafe.Pointer(tools))
	if rc := C.needle_init(system, tools, nil); rc < 0 {
		return ProbeResult{}, fmt.Errorf("needle_init failed: %d", int(rc))
	}
	defer C.needle_reset()
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	input := C.CString(probeIntent)
	defer C.free(unsafe.Pointer(input))
	dim := int(C.needle_embed(input, nil, 0))
	if dim <= 0 || dim > 65536 {
		return ProbeResult{}, fmt.Errorf("invalid Needle embedding dimension: %d", dim)
	}
	vector := make([]float32, dim)
	if n := int(C.needle_embed(input, (*C.float)(unsafe.Pointer(&vector[0])), C.int(dim))); n != dim {
		return ProbeResult{}, fmt.Errorf("needle_embed returned %d, expected %d", n, dim)
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	if err := validateEmbedding(vector); err != nil {
		return ProbeResult{}, err
	}
	C.needle_reset()
	out := make([]byte, 16384)
	n := int(C.needle_complete(input, 256, (*C.char)(unsafe.Pointer(&out[0])), C.int(len(out))))
	if n < 0 || n >= len(out) {
		return ProbeResult{}, fmt.Errorf("needle_complete failed or exceeded output capacity: %d", n)
	}
	if err := ctx.Err(); err != nil {
		return ProbeResult{}, err
	}
	// Read only within the Go-owned buffer even if the engine fails to terminate.
	end := 0
	for end < len(out) && out[end] != 0 {
		end++
	}
	if end == len(out) {
		return ProbeResult{}, fmt.Errorf("unterminated Needle proposal")
	}
	proposal := string(out[:end])
	if err := validateProposal(proposal); err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{ModelID: ModelID, Embedding: vector, Proposal: proposal}, nil
}
