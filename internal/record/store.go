package record

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// FileName is the JSONL file inside the tokentrace directory.
const FileName = "records.jsonl"

// EnvDir is the environment variable overriding the default directory.
const EnvDir = "TOKENTRACE_DIR"

// Dir returns the directory records are stored in: $TOKENTRACE_DIR if set,
// otherwise ~/.tokentrace.
func Dir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".tokentrace"), nil
}

// DefaultPath returns the full path of the records file.
func DefaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Store appends records to a JSONL file, one JSON object per line.
// It is safe for concurrent use.
type Store struct {
	mu sync.Mutex
	f  *os.File
}

// Open creates dir if needed and opens the records file for appending.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("open store: empty directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{f: f}, nil
}

// Path returns the file the store appends to.
func (s *Store) Path() string { return s.f.Name() }

// Append writes one record as a single JSON line.
func (s *Store) Append(r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	line = append(line, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.f.Write(line); err != nil {
		return fmt.Errorf("append record: %w", err)
	}
	return nil
}

// Close closes the underlying file.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.f.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

// ErrStop may be returned from a Scan callback to stop early without error.
var ErrStop = errors.New("record: stop scanning")

// Scan streams records from a JSONL file line by line without loading it
// into memory, invoking fn for every valid record. Lines that fail to parse
// (for example a line truncated by a crash) are skipped; skipped reports
// how many.
func Scan(path string, fn func(Record) error) (skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 64*1024)
	for {
		line, readErr := br.ReadBytes('\n')
		if len(line) > 0 {
			var r Record
			if jsonErr := json.Unmarshal(line, &r); jsonErr != nil {
				skipped++
			} else if cbErr := fn(r); cbErr != nil {
				if errors.Is(cbErr, ErrStop) {
					return skipped, nil
				}
				return skipped, cbErr
			}
		}
		if readErr == io.EOF {
			return skipped, nil
		}
		if readErr != nil {
			return skipped, fmt.Errorf("read %s: %w", path, readErr)
		}
	}
}
