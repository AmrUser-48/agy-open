package session

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tokenusage "github.com/AmrUser-48/agy-open/internal/usage"
	"github.com/klauspost/compress/zstd"
)

// Message stores transcript entries and optional structured tool activity.
// Optional fields preserve compatibility with older JSONL conversation files.
type Message struct {
	Role       string    `json:"role"`
	Content    string    `json:"content,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Tool       string    `json:"tool,omitempty"`
	Target     string    `json:"target,omitempty"`
	Command    string    `json:"command,omitempty"`
	CallID     string    `json:"callId,omitempty"`
	WorkerID   string    `json:"workerId,omitempty"`
	WorkerRole string    `json:"workerRole,omitempty"`
	WorkerTask string    `json:"workerTask,omitempty"`
	WorkerStatus string  `json:"workerStatus,omitempty"`
	WorkerSnapshot string `json:"workerSnapshot,omitempty"`
	WorkerRepoRoot string `json:"workerRepoRoot,omitempty"`
	WorkerScope string `json:"workerScope,omitempty"`
	WorkerBaseline string `json:"workerBaseline,omitempty"`
	Arguments  string    `json:"arguments,omitempty"`
	Output     string    `json:"output,omitempty"`
	OK         bool      `json:"ok,omitempty"`
	Decision   string    `json:"decision,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	DurationMS int64     `json:"durationMs,omitempty"`
	// ProviderSteps stores raw AI Studio Interactions steps for stateless replay.
	// These internal records are omitted from the visible transcript.
	ProviderSteps []json.RawMessage `json:"providerSteps,omitempty"`
	// ProviderUsage stores raw, provider-reported token counters as internal
	// conversation metadata; it is not part of the visible/model transcript.
	ProviderUsage *tokenusage.TokenUsage `json:"providerUsage,omitempty"`
	// ContextSnapshot is a serialized active model-context view. Canonical
	// transcript and tool activity records remain untouched around this marker.
	ContextSnapshot []json.RawMessage `json:"contextSnapshot,omitempty"`
	ContextSnapshotCount int `json:"contextSnapshotCount,omitempty"`
}

// ShellCommand returns the exact shell command from current or legacy tool activity.
func (m Message) ShellCommand() string {
	if m.Kind != "tool_call" || m.Tool != "shell" {
		return ""
	}
	if command := strings.TrimSpace(m.Command); command != "" {
		return m.Command
	}
	if strings.TrimSpace(m.Arguments) != "" {
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(m.Arguments), &args); err == nil {
			if strings.TrimSpace(args.Command) != "" {
				return args.Command
			}
		}
	}
	if target := strings.TrimSpace(m.Target); target != "" && target != "shell" {
		return m.Target
	}
	return ""
}

// NormalizeShellCommand upgrades legacy history entries in memory. It does not
// rewrite the on-disk conversation file.
func (m *Message) NormalizeShellCommand() {
	if m == nil || (m.Tool != "shell" || m.Kind != "tool_call") || strings.TrimSpace(m.Command) != "" {
		return
	}
	if command := m.ShellCommand(); command != "" {
		m.Command = command
	}
}

type Store struct {
	file        *os.File
	writer      io.WriteCloser
	flush       func() error
	path        string
	compression string
	hasContent  bool
}

func New(compression string) (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".agy", "history")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	ts := time.Now().UTC().Format("20060102T150405.000Z")
	normalized := strings.ToLower(strings.TrimSpace(compression))
	if normalized == "lzo" {
		// Historical builds wrote Zstandard frames with a misleading .lzo suffix.
		// Keep accepting the old setting, but never create new mislabeled files.
		normalized = "zstd"
	}
	if normalized == "" {
		normalized = "gzip"
	}
	var ext string
	switch normalized {
	case "zstd", "zst":
		ext = ".jsonl.zst"
	case "gzip", "gz":
		ext = ".jsonl.gz"
	case "none", "off":
		ext = ".jsonl"
	default:
		ext = ".jsonl.zst"
	}
	p := filepath.Join(dir, ts+ext)
	return &Store{path: p, compression: normalized}, nil
}

func (s *Store) initFile() error {
	if s.file != nil {
		return nil
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	s.file = f
	if strings.HasSuffix(s.path, ".gz") {
		gw := gzip.NewWriter(f)
		s.writer = gw
		s.flush = func() error { return gw.Flush() }
	} else if strings.HasSuffix(s.path, ".zst") || strings.HasSuffix(s.path, ".lzo") {
		zw, err := zstd.NewWriter(f, zstd.WithEncoderLevel(zstd.SpeedFastest))
		if err != nil {
			_ = f.Close()
			return err
		}
		s.writer = zw
		s.flush = func() error { return zw.Flush() }
	}
	return nil
}

func (s *Store) Add(m Message) error {
	if err := s.initFile(); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	line := append(b, byte(10))
	if s.writer != nil {
		if _, err := s.writer.Write(line); err != nil {
			return err
		}
		if s.flush != nil {
			if err := s.flush(); err != nil {
				return err
			}
		}
		s.hasContent = true
		return nil
	}
	_, err = s.file.Write(line)
	if err != nil {
		return err
	}
	s.hasContent = true
	return nil
}

func (s *Store) Path() string { return s.path }
func (s *Store) Close() error {
	var closeErr error
	if s.writer != nil {
		writer := s.writer
		s.writer = nil
		if err := writer.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close session compressor: %w", err))
		}
	}
	if s.file != nil {
		file := s.file
		s.file = nil
		if err := file.Close(); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close session file: %w", err))
		}
	}
	if !s.hasContent {
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			closeErr = errors.Join(closeErr, fmt.Errorf("remove empty session: %w", err))
		}
	}
	return closeErr
}

type Entry struct {
	ID      string
	Path    string
	ModTime time.Time
}

func (s *Store) ID() string {
	name := filepath.Base(s.path)
	name = strings.TrimSuffix(name, ".zst")
	name = strings.TrimSuffix(name, ".gz")
	name = strings.TrimSuffix(name, ".lzo")
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func Recent(limit int) ([]Entry, error) {
	home, err := os.UserHomeDir()
	if err != nil { return nil, err }
	dir := filepath.Join(home, ".agy", "history")
	items, err := os.ReadDir(dir)
	if os.IsNotExist(err) { return nil, nil }
	if err != nil { return nil, err }
	entries := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.IsDir() { continue }
		name := item.Name()
		p := filepath.Join(dir, name)
		info, err := item.Info()
		if err != nil || info.Size() == 0 { continue }
		var id string
		if strings.HasSuffix(name, ".jsonl.zst") || strings.HasSuffix(name, ".jsonl.lzo") {
			id = strings.TrimSuffix(name, filepath.Ext(name))
			id = strings.TrimSuffix(id, ".jsonl")
		} else if strings.HasSuffix(name, ".jsonl.gz") {
			id = strings.TrimSuffix(name, ".jsonl.gz")
		} else if strings.HasSuffix(name, ".jsonl") {
			id = strings.TrimSuffix(name, ".jsonl")
		} else {
			continue
		}
		entries = append(entries, Entry{
			ID:      id,
			Path:    p,
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ID > entries[j].ID
	})
	if limit > 0 && len(entries) > limit { entries = entries[:limit] }
	return entries, nil
}

func Find(id string) (Entry, bool, error) {
	entries, err := Recent(0)
	if err != nil { return Entry{}, false, err }
	for _, entry := range entries {
		if entry.ID == id { return entry, true, nil }
	}
	return Entry{}, false, nil
}

func readSessionRecords(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil, nil
	}

	var reader io.Reader = f
	if strings.HasSuffix(path, ".zst") || strings.HasSuffix(path, ".lzo") {
		zr, err := zstd.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		reader = zr
	} else if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}

	var records []Message
	decoder := json.NewDecoder(reader)
	for {
		var message Message
		if err := decoder.Decode(&message); err != nil {
			// Preserve LoadRecent's compatibility with truncated final records.
			break
		}
		records = append(records, message)
	}
	return records, nil
}

func isInternalSessionRecord(message Message) bool {
	return message.Kind == "provider_usage" || message.Kind == "context_usage" || message.Kind == "context_snapshot"
}

func LoadRecent(path string, limit int) ([]Message, error) {
	records, err := readSessionRecords(path)
	if err != nil {
		return nil, err
	}
	messages := make([]Message, 0, len(records))
	for _, message := range records {
		if isInternalSessionRecord(message) {
			continue
		}
		message.NormalizeShellCommand()
		messages = append(messages, message)
	}
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	return messages, nil
}

// LoadActiveContext returns the most recent compacted active-context snapshot
// and the canonical records written after that snapshot. If there is no
// snapshot, tail contains the latest complete visible history window.
func LoadActiveContext(path string, limit int) (*Message, []Message, error) {
	records, err := readSessionRecords(path)
	if err != nil {
		return nil, nil, err
	}
	snapshotIndex := -1
	var snapshot *Message
	for index := range records {
		if records[index].Kind != "context_snapshot" {
			continue
		}
		copySnapshot := records[index]
		snapshot = &copySnapshot
		snapshotIndex = index
	}

	start := snapshotIndex + 1
	tail := make([]Message, 0, len(records)-start)
	for _, message := range records[start:] {
		if message.Kind == "context_snapshot" || message.Kind == "context_usage" {
			continue
		}
		if message.Kind == "provider_usage" {
			if snapshot == nil {
				continue
			}
		} else {
			message.NormalizeShellCommand()
		}
		tail = append(tail, message)
	}
	if snapshot == nil && limit > 0 && len(tail) > limit {
		tail = tail[len(tail)-limit:]
	}
	return snapshot, tail, nil
}

func Open(path string, compression string) *Store {
	return &Store{path: path, compression: compression, hasContent: true}
}


// LoadProviderUsage scans a complete session file without retaining the
// transcript in memory. Legacy sessions without usage metadata return zero
// totals. Corrupt/incomplete trailing records are handled like LoadRecent.
func LoadProviderUsage(path string) (*tokenusage.TokenUsage, tokenusage.Totals, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, tokenusage.Totals{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, tokenusage.Totals{}, err
	}
	if info.Size() == 0 {
		return nil, tokenusage.Totals{}, nil
	}

	var reader io.Reader = f
	if strings.HasSuffix(path, ".zst") || strings.HasSuffix(path, ".lzo") {
		zr, err := zstd.NewReader(f)
		if err != nil {
			return nil, tokenusage.Totals{}, err
		}
		defer zr.Close()
		reader = zr
	} else if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, tokenusage.Totals{}, err
		}
		defer gz.Close()
		reader = gz
	}

	var last *tokenusage.TokenUsage
	var totals tokenusage.Totals
	decoder := json.NewDecoder(reader)
	for {
		var entry struct {
			Kind          string                  `json:"kind"`
			ProviderUsage *tokenusage.TokenUsage `json:"providerUsage"`
		}
		if err := decoder.Decode(&entry); err != nil {
			break
		}
		if (entry.Kind != "provider_usage" && entry.Kind != "context_usage") || entry.ProviderUsage == nil {
			continue
		}
		if entry.Kind == "provider_usage" {
			last = entry.ProviderUsage.Clone()
		}
		totals.Add(entry.ProviderUsage)
	}
	return last, totals, nil
}
