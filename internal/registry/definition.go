// Package registry stores fixed-argv tools and their local raw-cosine index.
// Validation here is structural only. Execution requires a separate policy check.
package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxDefinitionBytes = 64 * 1024

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

type Definition struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Command     []string `json:"command"`
}

type Tool struct {
	Definition
	ContentHash string `json:"content_hash"`
	Trusted     bool   `json:"trusted"`
	Enabled     bool   `json:"enabled"`
	ModelID     string `json:"model_id"`
	Dimensions  int    `json:"dimensions"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

type Candidate struct {
	Tool     Tool    `json:"tool"`
	Distance float64 `json:"distance"`
}

// Parse accepts one strict JSON object of at most 64 KiB, including whitespace.
func Parse(r io.Reader) (Definition, error) {
	var d Definition
	b, err := io.ReadAll(io.LimitReader(r, MaxDefinitionBytes+1))
	if err != nil {
		return d, err
	}
	if len(b) > MaxDefinitionBytes {
		return d, errors.New("definition exceeds 64 KiB")
	}
	if !utf8.Valid(b) {
		return d, errors.New("definition is not UTF-8")
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, fmt.Errorf("decode definition: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return d, errors.New("definition must contain exactly one JSON object")
	}
	return d, d.Validate()
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func (d Definition) Validate() error {
	if len(d.Name) > 64 || !namePattern.MatchString(d.Name) {
		return errors.New("name must be snake_case, at most 64 bytes")
	}
	if len(d.Description) > 4096 || strings.TrimSpace(d.Description) == "" || !printable(d.Description) {
		return errors.New("description must be nonempty printable text, at most 4096 bytes")
	}
	if len(d.Command) == 0 || len(d.Command) > 64 {
		return errors.New("command must have 1 to 64 fixed argv tokens")
	}
	total := 0
	for i, token := range d.Command {
		total += len(token)
		if len(token) > 4096 || !printable(token) || strings.ContainsAny(token, "{}") || (i == 0 && strings.TrimSpace(token) == "") {
			return errors.New("command contains invalid, oversized, or placeholder token")
		}
	}
	if total > 16384 {
		return errors.New("command argv exceeds 16 KiB")
	}
	return nil
}

func contentHash(d Definition) string {
	b, _ := json.Marshal(d)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
