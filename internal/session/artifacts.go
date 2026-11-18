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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	// OutputArtifactInlineLimit is the largest result kept entirely in the
	// active model context. Larger results are saved as session artifacts.
	OutputArtifactInlineLimit = 12 << 10
	// OutputArtifactReadLimit bounds one artifact read returned to a model.
	OutputArtifactReadLimit = 8 << 10
	// OutputArtifactSearchWindow bounds the bytes scanned by one search call.
	OutputArtifactSearchWindow = 8 << 20
	artifactPreviewHeadBytes = 6 << 10
	artifactPreviewTailBytes = 2 << 10
	artifactSearchDefaultMatches = 20
	artifactSearchMaxMatches = 40
)

var outputArtifactIDPattern = regexp.MustCompile("^out-[a-f0-9]{16}$")

// Artifact describes a complete tool output stored beside its owning session.
// Path is deliberately not exposed: callers address artifacts only by ID.
type Artifact struct {
	ID      string
	Tool    string
	Size    int64
	SHA256  string
	Preview string
}

// ArtifactChunk is a bounded byte range read from a session artifact.
type ArtifactChunk struct {
	ID         string
	Offset     int64
	NextOffset int64
	Size       int64
	Content    string
	Complete   bool
}

// ArtifactMatch identifies a literal byte-string match in an artifact.
type ArtifactMatch struct {
	Offset  int64
	Snippet string
}

// ArtifactSearch is a bounded search page. NextOffset is the continuation
// point for another search call when Complete is false.
type ArtifactSearch struct {
	ID         string
	Offset     int64
	NextOffset int64
	Size       int64
	Matches    []ArtifactMatch
	Complete   bool
}

// ArtifactCapture streams command output to a private temporary file while
// retaining only a small head/tail preview in memory.
type ArtifactCapture struct {
	mu        sync.Mutex
	id        string
	tool      string
	tempPath  string
	finalPath string
	file      *os.File
	digest    hash.Hash
	size      int64
	head      []byte
	tail      []byte
	err       error
	closed    bool
}

func (s *Store) artifactDirectory() (string, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return "", errors.New("session artifact storage is unavailable")
	}
	dir := s.path + ".artifacts"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create session artifact directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("inspect session artifact directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("session artifact path is not a real directory")
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return "", fmt.Errorf("secure session artifact directory: %w", err)
	}
	return dir, nil
}

func newOutputArtifactID() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate output artifact ID: %w", err)
	}
	return "out-" + hex.EncodeToString(random[:]), nil
}

// BeginArtifactCapture creates a private spool file. Call Finalize after the
// producer exits; call Abort when the producer cannot finish normally.
func (s *Store) BeginArtifactCapture(tool string) (*ArtifactCapture, error) {
	dir, err := s.artifactDirectory()
	if err != nil {
		return nil, err
	}
	id, err := newOutputArtifactID()
	if err != nil {
		return nil, err
	}
	tempPath := filepath.Join(dir, "."+id+".tmp")
	finalPath := filepath.Join(dir, id+".out")
	file, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create output artifact spool: %w", err)
	}
	return &ArtifactCapture{
		id: id, tool: strings.TrimSpace(tool), tempPath: tempPath,
		finalPath: finalPath, file: file, digest: sha256.New(),
		head: make([]byte, 0, artifactPreviewHeadBytes),
		tail: make([]byte, 0, artifactPreviewTailBytes),
	}, nil
}

// Write implements io.Writer. It is safe for stdout/stderr copy goroutines
// writing concurrently to the same capture.
func (c *ArtifactCapture) Write(p []byte) (int, error) {
	if c == nil {
		return 0, errors.New("nil output artifact capture")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.file == nil {
		return 0, os.ErrClosed
	}
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.file.Write(p)
	if n > 0 {
		_, _ = c.digest.Write(p[:n])
		c.size += int64(n)
		if len(c.head) < artifactPreviewHeadBytes {
			remaining := artifactPreviewHeadBytes - len(c.head)
			take := n
			if take > remaining {
				take = remaining
			}
			c.head = append(c.head, p[:take]...)
		}
		if n >= artifactPreviewTailBytes {
			c.tail = append(c.tail[:0], p[n-artifactPreviewTailBytes:n]...)
		} else {
			c.tail = append(c.tail, p[:n]...)
			if len(c.tail) > artifactPreviewTailBytes {
				c.tail = append(c.tail[:0], c.tail[len(c.tail)-artifactPreviewTailBytes:]...)
			}
		}
	}
	if err != nil {
		c.err = err
	}
	return n, err
}

// Size reports the number of bytes successfully spooled so far.
func (c *ArtifactCapture) Size() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.size
}

// Finalize closes the spool. Small results are removed and return nil because
// their complete content remains inline. Large results become durable files.
func (c *ArtifactCapture) Finalize() (*Artifact, error) {
	if c == nil {
		return nil, errors.New("nil output artifact capture")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, os.ErrClosed
	}
	c.closed = true
	if c.file != nil {
		if err := c.file.Sync(); err != nil && c.err == nil {
			c.err = err
		}
		if err := c.file.Close(); err != nil && c.err == nil {
			c.err = err
		}
		c.file = nil
	}
	if c.err != nil {
		_ = os.Remove(c.tempPath)
		return nil, fmt.Errorf("finish output artifact: %w", c.err)
	}
	if c.size <= OutputArtifactInlineLimit {
		if err := os.Remove(c.tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("remove inline output spool: %w", err)
		}
		return nil, nil
	}
	if err := os.Rename(c.tempPath, c.finalPath); err != nil {
		_ = os.Remove(c.tempPath)
		return nil, fmt.Errorf("commit output artifact: %w", err)
	}
	return &Artifact{
		ID: c.id, Tool: c.tool, Size: c.size,
		SHA256: hex.EncodeToString(c.digest.Sum(nil)),
		Preview: c.previewLocked(),
	}, nil
}

// Abort removes an unfinished spool without publishing a partial artifact.
func (c *ArtifactCapture) Abort() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	var closeErr error
	if c.file != nil {
		closeErr = c.file.Close()
		c.file = nil
	}
	removeErr := os.Remove(c.tempPath)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	return errors.Join(closeErr, removeErr)
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

// SaveArtifact stores a completed tool result when it exceeds the inline
// threshold. Small results return nil and remain complete in the transcript.
func (s *Store) SaveArtifact(tool, output string) (*Artifact, error) {
	if len(output) <= OutputArtifactInlineLimit {
		return nil, nil
	}
	capture, err := s.BeginArtifactCapture(tool)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(capture, output); err != nil {
		_ = capture.Abort()
		return nil, fmt.Errorf("write output artifact: %w", err)
	}
	artifact, err := capture.Finalize()
	if err != nil {
		_ = capture.Abort()
		return nil, err
	}
	return artifact, nil
}

// HasArtifacts reports whether this session has at least one committed
// output artifact. It is used to advertise retrieval tools only when useful.
func (s *Store) HasArtifacts() bool {
	dir := s.path + ".artifacts"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".out") {
			continue
		}
		id := strings.TrimSuffix(name, ".out")
		if !outputArtifactIDPattern.MatchString(id) || entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			continue
		}
		return true
	}
	return false
}

func (s *Store) artifactFile(id string) (*os.File, os.FileInfo, error) {
	if !outputArtifactIDPattern.MatchString(id) {
		return nil, nil, errors.New("invalid output artifact ID")
	}
	dir, err := s.artifactDirectory()
	if err != nil {
		return nil, nil, err
	}
	path := filepath.Join(dir, id+".out")
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open output artifact %s: %w", id, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("output artifact is not a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open output artifact %s: %w", id, err)
	}
	return file, info, nil
}

// ReadArtifact returns one bounded byte range. Offsets are byte-based so a
// caller can continue without loading a large artifact into memory.
func (s *Store) ReadArtifact(id string, offset int64, maxBytes int) (ArtifactChunk, error) {
	if maxBytes <= 0 {
		maxBytes = 12 << 10
	}
	if maxBytes > OutputArtifactReadLimit {
		maxBytes = OutputArtifactReadLimit
	}
	file, info, err := s.artifactFile(id)
	if err != nil {
		return ArtifactChunk{}, err
	}
	defer file.Close()
	size := info.Size()
	if offset < 0 || offset > size {
		return ArtifactChunk{}, fmt.Errorf("artifact offset must be between 0 and %d", size)
	}
	length := int64(maxBytes)
	if remaining := size - offset; length > remaining {
		length = remaining
	}
	data := make([]byte, int(length))
	n, readErr := file.ReadAt(data, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ArtifactChunk{}, fmt.Errorf("read output artifact: %w", readErr)
	}
	data = data[:n]
	next := offset + int64(n)
	return ArtifactChunk{
		ID: id, Offset: offset, NextOffset: next, Size: size,
		Content: strings.ToValidUTF8(string(data), "�"),
		Complete: next >= size,
	}, nil
}

// SearchArtifact performs a bounded literal byte search. For large artifacts,
// callers continue from NextOffset until Complete is true.
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
	file, info, err := s.artifactFile(id)
	if err != nil {
		return ArtifactSearch{}, err
	}
	defer file.Close()
	size := info.Size()
	if offset < 0 || offset > size {
		return ArtifactSearch{}, fmt.Errorf("artifact offset must be between 0 and %d", size)
	}
	if offset == size {
		return ArtifactSearch{ID: id, Offset: offset, NextOffset: offset, Size: size, Complete: true}, nil
	}
	window := int64(OutputArtifactSearchWindow)
	if remaining := size - offset; window > remaining {
		window = remaining
	}
	data := make([]byte, int(window))
	n, readErr := file.ReadAt(data, offset)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ArtifactSearch{}, fmt.Errorf("read output artifact for search: %w", readErr)
	}
	data = data[:n]
	needle := []byte(query)
	search := ArtifactSearch{
		ID: id, Offset: offset, Size: size,
		Matches: make([]ArtifactMatch, 0, maxMatches),
		Complete: offset+int64(n) >= size,
	}
	position := 0
	for position <= len(data)-len(needle) {
		relative := bytes.Index(data[position:], needle)
		if relative < 0 {
			break
		}
		matchAt := position + relative
		snippetStart := matchAt - 120
		if snippetStart < 0 {
			snippetStart = 0
		}
		snippetEnd := matchAt + len(needle) + 120
		if snippetEnd > len(data) {
			snippetEnd = len(data)
		}
		snippet := strings.ToValidUTF8(string(data[snippetStart:snippetEnd]), "�")
		search.Matches = append(search.Matches, ArtifactMatch{
			Offset: offset + int64(matchAt), Snippet: snippet,
		})
		position = matchAt + 1
		if len(search.Matches) >= maxMatches {
			search.NextOffset = offset + int64(position)
			search.Complete = search.NextOffset >= size
			return search, nil
		}
	}
	search.NextOffset = offset + int64(n)
	if !search.Complete && len(needle) > 1 {
		overlap := int64(len(needle) - 1)
		if search.NextOffset-overlap > offset {
			search.NextOffset -= overlap
		}
	}
	return search, nil
}

// ArtifactText returns text suitable for tool results, including an explicit
// continuation offset when the search page did not cover the full artifact.
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
