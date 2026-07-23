package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// memStorage is an in-memory ObjectStorage used to exercise archive
// rebuild/eviction without a real S3 endpoint.
type memStorage struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    []string
}

func newMemStorage() *memStorage {
	return &memStorage{objects: map[string][]byte{}}
}

func (m *memStorage) Head(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return 0, fmt.Errorf("object not found: %s", key)
	}
	return int64(len(data)), nil
}

func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStorage) Put(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	m.puts = append(m.puts, key)
	return nil
}

func (m *memStorage) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.objects[key]; !ok {
		return fmt.Errorf("object not found: %s", key)
	}
	delete(m.objects, key)
	return nil
}

func (m *memStorage) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://fake.storage/" + key, nil
}

func (m *memStorage) PresignPut(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://fake.storage/upload/" + key, nil
}

func (m *memStorage) putCountWithPrefix(prefix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, key := range m.puts {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}

func (m *memStorage) object(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	return data, ok
}
