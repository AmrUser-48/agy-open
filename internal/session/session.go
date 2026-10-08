package session

import (
	"bufio"
	"encoding/json"
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
	name := time.Now().UTC().Format("20060102T150405.000Z") + ".jsonl"
	p := filepath.Join(dir, name)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &Store{file: f, path: p}, nil
}

func (s *Store) Add(m Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = s.file.Write(append(b, byte(10)))
	return err
}

func (s *Store) Path() string { return s.path }
func (s *Store) Close() error { return s.file.Close() }

type Entry struct {
	ID      string
	Path    string
	ModTime time.Time
}

func (s *Store) ID() string {
	return strings.TrimSuffix(filepath.Base(s.path), filepath.Ext(s.path))
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
		if item.IsDir() || !strings.HasSuffix(item.Name(), ".jsonl") { continue }
		info, err := item.Info()
		if err != nil { continue }
		entries = append(entries, Entry{
			ID: strings.TrimSuffix(item.Name(), ".jsonl"),
			Path: filepath.Join(dir, item.Name()),
			ModTime: info.ModTime(),
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ModTime.After(entries[j].ModTime) })
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
	var all []Message
	sc := bufio.NewScanner(f)
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
