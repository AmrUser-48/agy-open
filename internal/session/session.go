package session

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
	_, err = s.file.Write(append(b, '
'))
	return err
}

func (s *Store) Path() string { return s.path }
func (s *Store) Close() error { return s.file.Close() }

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
