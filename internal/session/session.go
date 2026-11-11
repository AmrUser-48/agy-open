package session

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
	Arguments  string    `json:"arguments,omitempty"`
	Output     string    `json:"output,omitempty"`
	OK         bool      `json:"ok,omitempty"`
	Decision   string    `json:"decision,omitempty"`
	StartedAt  time.Time `json:"startedAt,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
	DurationMS int64     `json:"durationMs,omitempty"`
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
	var ext string
	switch normalized {
	case "zstd", "zst", "":
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
	if s.writer != nil {
		_ = s.writer.Close()
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	if !s.hasContent {
		_ = os.Remove(s.path)
	}
	return nil
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

func LoadRecent(path string, limit int) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return nil, nil
	}

	var r io.Reader = f
	if strings.HasSuffix(path, ".zst") || strings.HasSuffix(path, ".lzo") {
		zsr, err := zstd.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer zsr.Close()
		r = zsr
	} else if strings.HasSuffix(path, ".gz") {
		gzr, err := gzip.NewReader(f)
		if err != nil {
			return nil, err
		}
		defer gzr.Close()
		r = gzr
	}

	var msgs []Message
	dec := json.NewDecoder(r)
	for {
		var m Message
		if err := dec.Decode(&m); err != nil {
			break
		}
		m.NormalizeShellCommand()
		msgs = append(msgs, m)
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func Open(path string, compression string) *Store {
	return &Store{path: path, compression: compression, hasContent: true}
}
