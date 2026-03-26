package recorder

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type JSONLWriter struct {
	mu   sync.Mutex
	file *os.File
	path string
}

func NewJSONLWriter(path string) (*JSONLWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return &JSONLWriter{file: f, path: path}, nil
}

func (w *JSONLWriter) Write(rec Record) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	data = append(data, '\n')
	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("write record: %w", err)
	}
	return nil
}

func (w *JSONLWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}

func (w *JSONLWriter) FilePath() string {
	return w.path
}
