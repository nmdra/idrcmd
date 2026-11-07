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
	"time"
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

	if _, err := loadLocked(ctx, modelPath); err != nil {
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

// loadLocked requires nativeGate.
func loadLocked(ctx context.Context, modelPath string) (LoadMetrics, error) {
	var metrics LoadMetrics
	// Verify even when already loaded, so an invalid requested path never succeeds
	// because a previous probe happened to initialize the process-global model.
	verifyStart := time.Now()
	fd, err := syscall.Open(modelPath, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return metrics, fmt.Errorf("open Needle weights: %w", err)
	}
	f := os.NewFile(uintptr(fd), modelPath)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return metrics, fmt.Errorf("inspect Needle weights: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != modelSize {
		f.Close()
		return metrics, fmt.Errorf("Needle weights must be a regular file of pinned size")
	}
	data, err := io.ReadAll(io.LimitReader(f, modelSize+1))
	closeErr := f.Close()
	if err != nil {
		return metrics, fmt.Errorf("read Needle weights: %w", err)
	}
	if closeErr != nil {
		return metrics, closeErr
	}
	if len(data) != modelSize || fmt.Sprintf("%x", sha256.Sum256(data)) != modelSHA256 {
		return metrics, fmt.Errorf("Needle weights do not match pinned size and SHA256")
	}
	if err := ctx.Err(); err != nil {
		return metrics, err
	}
	metrics.VerifyDuration = time.Since(verifyStart)
	if loadFailure != nil {
		return metrics, loadFailure
	}
	if loadedWeights == nil {
		p := C.CBytes(data)
		if p == nil {
			return metrics, fmt.Errorf("allocate Needle weights")
		}
		loadStart := time.Now()
		rc := C.needle_load((*C.uchar)(p), C.ulonglong(len(data)))
		metrics.NativeLoadDuration = time.Since(loadStart)
		// The ABI does not document failure-path ownership either. Retain the buffer
		// even on a native load failure rather than risk use-after-free.
		loadedWeights = p
		if rc < 0 {
			loadFailure = fmt.Errorf("needle_load failed: %d (restart process before retry)", int(rc))
			return metrics, loadFailure
		}
	}
	if err := ctx.Err(); err != nil {
		return metrics, err
	}

	return metrics, nil
}

func openModel(ctx context.Context, modelPath string) (*Model, LoadMetrics, error) {
	if err := lockNative(ctx); err != nil {
		return nil, LoadMetrics{}, err
	}
	defer func() { <-nativeGate }()
	metrics, err := loadLocked(ctx, modelPath)
	if err != nil {
		return nil, metrics, err
	}
	return &Model{ready: true}, metrics, nil
}

func lockNative(ctx context.Context) error {
	select {
	case nativeGate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-nativeGate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

const cautiousSystem = "Select only from the provided tools. Never execute commands. Use only argument values explicitly supported by the user's request. Do not invent missing filenames, paths, names, or settings. Omit missing arguments. Return no tool calls for unsupported or ambiguous requests."

// initLocked discards previous conversation state before applying each schema.
func initLocked(ctx context.Context, toolsJSON string) (func(), error) {
	C.needle_reset()
	system := C.CString(cautiousSystem)
	tools := C.CString(toolsJSON)
	// The pinned header does not promise that init copies these strings. Keep
	// them alive through inference and reset, just as the original Probe does.
	release := func() {
		C.free(unsafe.Pointer(system))
		C.free(unsafe.Pointer(tools))
	}
	if rc := C.needle_init(system, tools, nil); rc < 0 {
		C.needle_reset()
		release()
		return nil, fmt.Errorf("needle_init failed: %d", int(rc))
	}
	if err := ctx.Err(); err != nil {
		C.needle_reset()
		release()
		return nil, err
	}
	return release, nil
}

func (m *Model) embed(ctx context.Context, text string) ([]float32, time.Duration, error) {
	if m == nil || !m.ready {
		return nil, 0, fmt.Errorf("Needle model is not open")
	}
	if err := lockNative(ctx); err != nil {
		return nil, 0, err
	}
	defer func() { <-nativeGate }()
	start := time.Now()
	vector, err := embedLocked(ctx, text)
	return vector, time.Since(start), err
}

func embedLocked(ctx context.Context, text string) ([]float32, error) {
	release, err := initLocked(ctx, "[]")
	if err != nil {
		return nil, err
	}
	defer release()
	defer C.needle_reset()
	input := C.CString(text)
	defer C.free(unsafe.Pointer(input))
	dim := int(C.needle_embed(input, nil, 0))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dim <= 0 || dim > 65536 {
		return nil, fmt.Errorf("invalid Needle embedding dimension: %d", dim)
	}
	vector := make([]float32, dim)
	n := int(C.needle_embed(input, (*C.float)(unsafe.Pointer(&vector[0])), C.int(dim)))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if n != dim {
		return nil, fmt.Errorf("needle_embed returned %d, expected %d", n, dim)
	}
	if err := validateEmbedding(vector); err != nil {
		return nil, err
	}
	return vector, nil
}

func (m *Model) complete(ctx context.Context, request, toolsJSON string) (string, time.Duration, error) {
	if m == nil || !m.ready {
		return "", 0, fmt.Errorf("Needle model is not open")
	}
	if err := lockNative(ctx); err != nil {
		return "", 0, err
	}
	defer func() { <-nativeGate }()
	start := time.Now()
	output, err := completeLocked(ctx, request, toolsJSON)
	return output, time.Since(start), err
}

func completeLocked(ctx context.Context, request, toolsJSON string) (string, error) {
	release, err := initLocked(ctx, toolsJSON)
	if err != nil {
		return "", err
	}
	defer release()
	defer C.needle_reset()
	input := C.CString(request)
	defer C.free(unsafe.Pointer(input))
	out := make([]byte, 16384)
	n := int(C.needle_complete(input, 512, (*C.char)(unsafe.Pointer(&out[0])), C.int(len(out))))
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if n < 0 || n >= len(out) {
		return "", fmt.Errorf("needle_complete failed or exceeded output capacity: %d", n)
	}
	for i, b := range out {
		if b == 0 {
			return string(out[:i]), nil
		}
	}
	return "", fmt.Errorf("unterminated Needle proposal")
}
