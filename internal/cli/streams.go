package cli

import "io"

// Escape terminal controls from all UI data, including filenames in errors.
// The external-tool path also sanitizes its bounded buffers before display.
type escapedStream struct{ writer io.Writer }

func (s escapedStream) Write(data []byte) (int, error) {
	text := safeOutput(string(data))
	n, err := io.WriteString(s.writer, text)
	if err != nil {
		return 0, err
	}
	if n != len(text) {
		return 0, io.ErrShortWrite
	}
	return len(data), nil
}
