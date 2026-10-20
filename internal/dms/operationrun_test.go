package dms

import (
	"context"
	"time"
)

// operationStep claims and advances one ready operation of any kind.
func (s *Service) operationStep(ctx context.Context) (bool, error) {
	return s.RunOperation(ctx, s.OperationKinds())
}

// runOperations drains every pending operation that is ready now.
func (s *Service) runOperations(ctx context.Context) error {
	for {
		worked, err := s.operationStep(ctx)
		if err != nil || !worked {
			return err
		}
	}
}

// workOperations runs operations in the background until ctx ends, as a
// worker does.
func (s *Service) workOperations(ctx context.Context) {
	for ctx.Err() == nil {
		if worked, _ := s.operationStep(ctx); !worked {
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Millisecond):
			}
		}
	}
}
