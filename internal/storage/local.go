package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"time"
)

var ErrTooLarge = errors.New("blob exceeds upload limit")

type Object struct {
	Key    string
	Size   int64
	SHA256 string
}
type ReadSeekCloser interface {
	io.ReadSeeker
	io.Closer
}
type Store interface {
	Put(context.Context, io.Reader, int64) (Object, error)
	Open(context.Context, string) (ReadSeekCloser, error)
	Delete(context.Context, string) error
	WalkObjects(context.Context, time.Time, func(string) error) error
}
type Local struct{ root string }

func NewLocal(root string) (*Local, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
func (s *Local) Put(ctx context.Context, input io.Reader, max int64) (Object, error) {
	if max < 1 {
		return Object{}, ErrTooLarge
	}
	f, err := os.CreateTemp(s.root, ".upload-*")
	if err != nil {
		return Object{}, err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(contextReader{ctx, input}, max+1))
	if err == nil && size > max {
		err = ErrTooLarge
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Object{}, err
	}
	key := uuid.NewString()
	if err = os.Rename(f.Name(), filepath.Join(s.root, key)); err != nil {
		return Object{}, err
	}
	return Object{key, size, hex.EncodeToString(h.Sum(nil))}, nil
}
func (s *Local) path(key string) (string, error) {
	if _, err := uuid.Parse(key); err != nil || len(key) != 36 {
		return "", errors.New("invalid object key")
	}
	return filepath.Join(s.root, key), nil
}
func (s *Local) Open(ctx context.Context, key string) (ReadSeekCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}
func (s *Local) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// WalkObjects visits completed objects older than before. Active upload files,
// directories and symlinks are excluded. WalkDir reads one directory at a time.
func (s *Local) WalkObjects(ctx context.Context, before time.Time, visit func(string) error) error {
	return filepath.WalkDir(s.root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != s.root {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if _, err = s.path(entry.Name()); err != nil {
			return nil
		}
		info, err := entry.Info()
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && info.ModTime().Before(before) {
			return visit(entry.Name())
		}
		return nil
	})
}
