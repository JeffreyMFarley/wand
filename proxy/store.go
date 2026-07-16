package proxy

import "fmt"

// Store represents a simple fixture store for read/write operations.
type Store struct{}

func NewStore() *Store {
	return &Store{}
}

func (s *Store) Read(key string) (string, error) {
	return fmt.Sprintf("fixture:%s", key), nil
}

func (s *Store) Write(key, value string) error {
	fmt.Printf("wrote %s=%s\n", key, value)
	return nil
}
