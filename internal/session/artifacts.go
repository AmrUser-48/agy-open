package session

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// OutputArtifactInlineLimit is the largest result kept entirely in the
	// active model context. Larger results are retained only in session memory.
	OutputArtifactInlineLimit = 12 << 10
	// OutputArtifactReadLimit bounds one artifact read returned to a model.
	OutputArtifactReadLimit = 8 << 10
	// OutputArtifactSearchWindow bounds the bytes scanned by one search call.
	OutputArtifactSearchWindow = 8 << 20
	// OutputArtifactMemoryLimit bounds one complete tool output retained in RAM.
	OutputArtifactMemoryLimit = 16 << 20
	// OutputArtifactSessionMemoryLimit bounds all retained outputs in one session.
	OutputArtifactSessionMemoryLimit int64 = 64 << 20
	artifactPreviewHeadBytes = 6 << 10
	artifactPreviewTailBytes = 2 << 10
	artifactSearchDefaultMatches = 20
	artifactSearchMaxMatches = 40
)

var (
	outputArtifactIDPattern = regexp.MustCompile("^out-[a-f0-9]{16}$")
	ErrArtifactMemoryLimit = errors.New("output artifact exceeds the in-memory retention limit")
)

type Artifact struct {
	ID      string
	Tool    string
	Size    int64
	SHA256  string
	Preview string
}

type ArtifactChunk struct {
	ID         string
	Offset     int64
	NextOffset int64
	Size       int64
	Content    string
	Complete   bool
}

type ArtifactMatch struct {
	Offset  int64
	Snippet string
}

type ArtifactSearch struct {
	ID         string
	Offset     int64
	NextOffset int64
	Size       int64
	Matches    []ArtifactMatch
	Complete   bool
}

type storedArtifact struct {
	metadata Artifact
	data     []byte
}

// ArtifactCapture retains complete output in bounded RAM. It never creates a
// temporary file or writes tool output to the filesystem.
type ArtifactCapture struct {
	mu       sync.Mutex
	store    *Store
	id       string
	tool     string
	data     []byte
	digest   hash.Hash
	size     int64
	head     []byte
	tail     []byte
	tooLarge bool
	closed   bool
}

func newOutputArtifactID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate output artifact ID: %w", err)
	}
	return "out-" + hex.EncodeToString(random[:]), nil
}

func (s *Store) BeginArtifactCapture(tool string) (*ArtifactCapture, error) {
	if s == nil {
		return nil, errors.New("session memory is unavailable")
	}
	id, err := newOutputArtifactID()
	if err != nil {
		return nil, err
	}
	return &ArtifactCapture{
		store: s,
		id: id,
		tool: strings.TrimSpace(tool),
		data: make([]byte, 0, OutputArtifactInlineLimit),
		digest: sha256.New(),
		head: make([]byte, 0, artifactPreviewHeadBytes),
		tail: make([]byte, 0, artifactPreviewTailBytes),
	}, nil
}

// Write implements io.Writer. It consumes the entire stream while retaining
// at most OutputArtifactMemoryLimit bytes for a later in-session read.
func (c *ArtifactCapture) Write(p []byte) (int, error) {
	if c == nil {
		return 0, errors.New("nil output artifact capture")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("output artifact capture is closed")
	}
	if len(p) == 0 {
		return 0, nil
	}

	_, _ = c.digest.Write(p)
	c.size += int64(len(p))

	if len(c.head) < artifactPreviewHeadBytes {
		take := len(p)
		if remaining := artifactPreviewHeadBytes - len(c.head); take > remaining {
			take = remaining
		}
		c.head = append(c.head, p[:take]...)
	}
	if len(p) >= artifactPreviewTailBytes {
		c.tail = append(c.tail[:0], p[len(p)-artifactPreviewTailBytes:]...)
	} else {
		c.tail = append(c.tail, p...)
		if len(c.tail) > artifactPreviewTailBytes {
			c.tail = append(c.tail[:0], c.tail[len(c.tail)-artifactPreviewTailBytes:]...)
		}
	}

	remaining := OutputArtifactMemoryLimit - len(c.data)
	if remaining > 0 {
		take := len(p)
		if take > remaining {
			take = remaining
		}
		c.data = append(c.data, p[:take]...)
		if take < len(p) {
			c.tooLarge = true
		}
	} else {
		c.tooLarge = true
	}
	return len(p), nil
}

func (c *ArtifactCapture) Size() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

func (c *ArtifactCapture) Finalize() (*Artifact, error) {
	if c == nil {
		return nil, errors.New("nil output artifact capture")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("output artifact capture is closed")
	}
	c.closed = true
	if c.size <= OutputArtifactInlineLimit {
		c.data = nil
		return nil, nil
	}
	if c.tooLarge {
		c.data = nil
		return nil, fmt.Errorf("%w: one output may retain at most %d bytes", ErrArtifactMemoryLimit, OutputArtifactMemoryLimit)
	}
	if c.store == nil {
		c.data = nil
		return nil, errors.New("session memory is unavailable")
	}

	artifact := Artifact{
		ID: c.id,
		Tool: c.tool,
		Size: c.size,
		SHA256: hex.EncodeToString(c.digest.Sum(nil)),
		Preview: c.previewLocked(),
	}
	data := c.data
	c.data = nil
	if err := c.store.addArtifact(artifact, data); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func (c *ArtifactCapture) Abort() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.data = nil
	return nil
}

func (c *ArtifactCapture) previewLocked() string {
	head := strings.ToValidUTF8(string(c.head), "�")
	tail := strings.ToValidUTF8(string(c.tail), "�")
	omitted := c.size - int64(len(c.head)) - int64(len(c.tail))
	if omitted < 0 {
		omitted = 0
	}
	return head + fmt.Sprintf("\n\n[... %d bytes omitted ...]\n\n", omitted) + tail
}

// ReserveInlineOutput accounts for a small tool result retained in the
// Agent's in-memory transcript. It shares the same per-output and per-session
// budgets as large artifacts, but never stores or writes the bytes itself.
func (s *Store) ReserveInlineOutput(size int64) error {
	if s == nil {
		return errors.New("session memory is unavailable")
	}
	if size < 0 {
		return errors.New("tool output size cannot be negative")
	}
	if size > OutputArtifactMemoryLimit {
		return fmt.Errorf("%w: one output may retain at most %d bytes", ErrArtifactMemoryLimit, OutputArtifactMemoryLimit)
	}
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	if size > OutputArtifactSessionMemoryLimit-s.artifactBytes {
		return fmt.Errorf("%w: session limit is %d bytes", ErrArtifactMemoryLimit, OutputArtifactSessionMemoryLimit)
	}
	s.artifactBytes += size
	return nil
}

// ClearArtifacts discards complete tool outputs and all inline-output budget
// reservations for the current session without touching its disk transcript.
func (s *Store) ClearArtifacts() {
	if s == nil { return }
	s.artifactMu.Lock()
	s.artifacts = nil
	s.artifactBytes = 0
	s.artifactMu.Unlock()
}

func (s *Store) addArtifact(artifact Artifact, data []byte) error {
	if s == nil {
		return errors.New("session memory is unavailable")
	}
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()
	if s.artifacts == nil {
		s.artifacts = make(map[string]storedArtifact)
	}
	if _, exists := s.artifacts[artifact.ID]; exists {
		return errors.New("output artifact ID collision")
	}
	if int64(len(data)) > OutputArtifactSessionMemoryLimit-s.artifactBytes {
		return fmt.Errorf("%w: session limit is %d bytes", ErrArtifactMemoryLimit, OutputArtifactSessionMemoryLimit)
	}
	s.artifacts[artifact.ID] = storedArtifact{metadata: artifact, data: data}
	s.artifactBytes += int64(len(data))
	return nil
}

// SaveArtifact retains a completed large tool result in RAM for this session.
// The bytes are discarded when the session closes.
func (s *Store) SaveArtifact(tool, output string) (*Artifact, error) {
	if len(output) <= OutputArtifactInlineLimit {
		return nil, nil
	}
	if len(output) > OutputArtifactMemoryLimit {
		return nil, fmt.Errorf("%w: one output may retain at most %d bytes", ErrArtifactMemoryLimit, OutputArtifactMemoryLimit)
	}
	capture, err := s.BeginArtifactCapture(tool)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(capture, output); err != nil {
		_ = capture.Abort()
		return nil, fmt.Errorf("capture output in memory: %w", err)
	}
	artifact, err := capture.Finalize()
	if err != nil {
		_ = capture.Abort()
		return nil, err
	}
	return artifact, nil
}

func (s *Store) HasArtifacts() bool {
	if s == nil {
		return false
	}
	s.artifactMu.RLock()
	defer s.artifactMu.RUnlock()
	return len(s.artifacts) > 0
}

func (s *Store) lookupArtifact(id string) (storedArtifact, error) {
	if !outputArtifactIDPattern.MatchString(id) {
		return storedArtifact{}, errors.New("invalid output artifact ID")
	}
	if s == nil {
		return storedArtifact{}, errors.New("session memory is unavailable")
	}
	s.artifactMu.RLock()
	artifact, ok := s.artifacts[id]
	s.artifactMu.RUnlock()
	if !ok {
		return storedArtifact{}, fmt.Errorf("output artifact %s is not available in this session", id)
	}
	return artifact, nil
}

func (s *Store) CopyArtifactTo(id string, dst io.Writer) (int64, error) {
	if dst == nil {
		return 0, errors.New("artifact destination writer is nil")
	}
	artifact, err := s.lookupArtifact(id)
	if err != nil {
		return 0, err
	}
	written, err := io.Copy(dst, bytes.NewReader(artifact.data))
	if err != nil {
		return written, fmt.Errorf("copy output artifact from memory: %w", err)
	}
	if written != int64(len(artifact.data)) {
		return written, fmt.Errorf("copy output artifact: copied %d of %d bytes", written, len(artifact.data))
	}
	return written, nil
}

func (s *Store) ReadArtifact(id string, offset int64, maxBytes int) (ArtifactChunk, error) {
	if maxBytes <= 0 {
		maxBytes = 12 << 10
	}
	if maxBytes > OutputArtifactReadLimit {
		maxBytes = OutputArtifactReadLimit
	}
	artifact, err := s.lookupArtifact(id)
	if err != nil {
		return ArtifactChunk{}, err
	}
	size := int64(len(artifact.data))
	if offset < 0 || offset > size {
		return ArtifactChunk{}, fmt.Errorf("artifact offset must be between 0 and %d", size)
	}
	end := offset + int64(maxBytes)
	if end > size {
		end = size
	}
	data := artifact.data[int(offset):int(end)]
	return ArtifactChunk{
		ID: id,
		Offset: offset,
		NextOffset: end,
		Size: size,
		Content: strings.ToValidUTF8(string(data), "�"),
		Complete: end >= size,
	}, nil
}

func (s *Store) SearchArtifact(id, query string, offset int64, maxMatches int) (ArtifactSearch, error) {
	if query == "" || len(query) > 1024 {
		return ArtifactSearch{}, errors.New("search query must contain 1–1024 bytes")
	}
	if maxMatches <= 0 {
		maxMatches = artifactSearchDefaultMatches
	}
	if maxMatches > artifactSearchMaxMatches {
		maxMatches = artifactSearchMaxMatches
	}
	artifact, err := s.lookupArtifact(id)
	if err != nil {
		return ArtifactSearch{}, err
	}
	data := artifact.data
	size := int64(len(data))
	if offset < 0 || offset > size {
		return ArtifactSearch{}, fmt.Errorf("artifact offset must be between 0 and %d", size)
	}
	if offset == size {
		return ArtifactSearch{ID: id, Offset: offset, NextOffset: offset, Size: size, Complete: true}, nil
	}
	windowEnd := offset + OutputArtifactSearchWindow
	if windowEnd > size {
		windowEnd = size
	}
	window := data[int(offset):int(windowEnd)]
	needle := []byte(query)
	search := ArtifactSearch{
		ID: id,
		Offset: offset,
		Size: size,
		Matches: make([]ArtifactMatch, 0, maxMatches),
		Complete: windowEnd >= size,
	}
	position := 0
	for position <= len(window)-len(needle) {
		relative := bytes.Index(window[position:], needle)
		if relative < 0 {
			break
		}
		matchAt := position + relative
		snippetStart := matchAt - 120
		if snippetStart < 0 {
			snippetStart = 0
		}
		snippetEnd := matchAt + len(needle) + 120
		if snippetEnd > len(window) {
			snippetEnd = len(window)
		}
		snippet := strings.ToValidUTF8(string(window[snippetStart:snippetEnd]), "�")
		search.Matches = append(search.Matches, ArtifactMatch{
			Offset: offset + int64(matchAt),
			Snippet: snippet,
		})
		position = matchAt + 1
		if len(search.Matches) >= maxMatches {
			search.NextOffset = offset + int64(position)
			search.Complete = search.NextOffset >= size
			return search, nil
		}
	}
	search.NextOffset = windowEnd
	if !search.Complete && len(needle) > 1 {
		overlap := int64(len(needle) - 1)
		if search.NextOffset-overlap > offset {
			search.NextOffset -= overlap
		}
	}
	return search, nil
}

func ArtifactSearchText(result ArtifactSearch) string {
	var out strings.Builder
	fmt.Fprintf(&out, "Artifact %s · %d bytes · search from byte %d\n", result.ID, result.Size, result.Offset)
	for _, match := range result.Matches {
		fmt.Fprintf(&out, "\nmatch at byte %d:\n%s\n", match.Offset, match.Snippet)
	}
	if len(result.Matches) == 0 {
		out.WriteString("No matches in this search window.\n")
	}
	if result.Complete {
		out.WriteString("\nSearch reached the end of the artifact.")
	} else {
		fmt.Fprintf(&out, "\nMore output remains. Continue with offset=%d.", result.NextOffset)
	}
	if !utf8.ValidString(out.String()) {
		return strings.ToValidUTF8(out.String(), "�")
	}
	return out.String()
}
