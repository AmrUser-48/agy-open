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
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Store struct {
	file *os.File
	gz   *gzip.Writer
	path string
}

func New() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".agy", "history")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format("20060102T150405.000Z") + ".jsonl.gz"
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	gz := gzip.NewWriter(f)
	return &Store{file: f, gz: gz, path: p}, nil
}

func (s *Store) Add(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := s.gz.Write(append(b, byte(10))); err != nil {
		return err
	}
	return s.gz.Flush()
}

func (s *Store) Path() string { return s.path }
func (s *Store) Close() error {
	if s.gz != nil {
		_ = s.gz.Close()
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
	name = strings.TrimSuffix(name, ".gz")
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
		if strings.HasSuffix(name, ".jsonl.gz") {
			id = strings.TrimSuffix(name, ".jsonl.gz")
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
	// Sort by ID descending (newer timestamps first on top)
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
	if strings.HasSuffix(path, ".gz") {
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
