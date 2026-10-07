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
