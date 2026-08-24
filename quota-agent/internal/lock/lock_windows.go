//go:build windows

package lock

import (
	"fmt"
	"os"
	"path/filepath"
)

type Lock struct {
	file *os.File
	path string
}

func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if os.IsExist(err) {
		return nil, fmt.Errorf("another quota agent may already be running")
	}
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	return &Lock{file: file, path: path}, nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Close()
	_ = os.Remove(l.path)
	return err
}
