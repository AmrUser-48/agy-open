package session

import (
	"bufio"
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

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Store struct {
	file   *os.File
	writer io.WriteCloser
	flush  func() error
	path   string
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
	var ext string
	switch strings.ToLower(compression) {
	case "zstd", "zst":
		ext = ".jsonl.zst"
	case "gzip", "gz":
		ext = ".jsonl.gz"
	case "lzo":
		ext = ".jsonl.lzo"
	case "none", "jsonl":
		ext = ".jsonl"
	default:
		ext = ".jsonl.zst"
	}
	p := filepath.Join(dir, ts+ext)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}

	var writer io.WriteCloser
	var flush func() error

	if strings.HasSuffix(ext, ".zst") {
		zw, err := zstd.NewWriter(f, zstd.WithSpeed(zstd.SpeedFastest))
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		writer = zw
		flush = func() error { return zw.Flush() }
	} else if strings.HasSuffix(ext, ".gz") {
		gw := gzip.NewWriter(f)
		writer = gw
		flush = func() error { return gw.Flush() }
	} else if strings.HasSuffix(ext, ".lzo") {
		zw, err := zstd.NewWriter(f, zstd.WithSpeed(zstd.SpeedFastest))
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		writer = zw
		flush = func() error { return zw.Flush() }
	}

	return &Store{file: f, writer: writer, flush: flush, path: p}, nil
}

func (s *Store) Add(m Message) error {
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
			return s.flush()
		}
		return nil
	}
	_, err = s.file.Write(line)
	return err
}

func (s *Store) Path() string { return s.path }
func (s *Store) Close() error {
	if s.writer != nil {
		_ = s.writer.Close()
	}
	if s.file != nil {
		return s.file.Close()
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
		var id string
		if strings.HasSuffix(name, ".jsonl.zst") {
			id = strings.TrimSuffix(name, ".jsonl.zst")
		} else if strings.HasSuffix(name, ".jsonl.gz") {
			id = strings.TrimSuffix(name, ".jsonl.gz")
		} else if strings.HasSuffix(name, ".jsonl.lzo") {
			id = strings.TrimSuffix(name, ".jsonl.lzo")
		} else if strings.HasSuffix(name, ".jsonl") {
			id = strings.TrimSuffix(name, ".jsonl")
		} else {
			continue
		}
		info, err := item.Info()
		if err != nil { continue }
		entries = append(entries, Entry{
			ID:      id,
			Path:    filepath.Join(dir, name),
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

	var all []Message
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		var m Message
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			all = append(all, m)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	return all, nil
}
