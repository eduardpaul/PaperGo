package dms

import (
	"context"
	"errors"
	"time"

	"papergo/internal/dal/operations"
)

// RunOperationGroup claims the ready members of one admission group of
// kind, up to limit distinct inputs created by through when it is set, and
// runs the kind's group execution on them outside the writer. Members whose
// creator lost access fail on their own. It reports whether it found work.
func (s *Service) RunOperationGroup(ctx context.Context, kind, group string, through *time.Time, limit int) (bool, error) {
	handler, ok := s.ops.handlers[kind]
	if !ok || handler.ExecuteGroup == nil {
		return false, invalid("operation kind " + kind + " does not run in groups")
	}
	var members []*operations.Operation
	worked := false
	err := s.write(ctx, func(t *Service) error {
		members, worked = nil, false
		claimed, e := t.DAL.Operations().Claim(ctx, operations.ClaimRequest{Ready: operations.Ready{Kinds: []string{kind}, GroupKey: &group, Through: through, Now: time.Now(), Limit: limit}, Holder: t.ops.holder, TTL: operationClaim})
		if e != nil {
			return e
		}
		worked = len(claimed) > 0
		for i := range claimed {
			op := &claimed[i]
			if op.Attempts > handler.maxAttempts() {
				if e = t.failClaimed(ctx, handler, op, operations.ErrorRetriesExhausted, errors.New("the operation's attempts ran out")); e != nil {
					return e
				}
				continue
			}
			opCtx, e := t.operationContext(ctx, op)
			if e == nil {
				e = t.operationPermission(opCtx, op.CreatedBy, op.ContainerID, handler.Scope)
			}
			if errors.Is(e, ErrForbidden) {
				if e = t.failClaimed(ctx, handler, op, operations.ErrorForbidden, ErrForbidden); e != nil {
					return e
				}
				continue
			}
			if e != nil {
				return e
			}
			if op.StartedAt == nil {
				started := time.Now().UTC()
				op.StartedAt = &started
				if e = t.SaveOperation(ctx, op); e != nil {
					return e
				}
			}
			members = append(members, op)
		}
		return nil
	})
	if err != nil || len(members) == 0 {
		return worked, err
	}
	err = s.executeClaimed(ctx, members, func(run context.Context) (err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("operation handler panicked")
			}
		}()
		return handler.ExecuteGroup(run, s, members)
	})
	if errors.Is(err, operations.ErrClaimLost) {
		return true, nil
	}
	// Members the execution left pending under its claims are given back
	// when it succeeded, and otherwise deferred or failed as its error says.
	for _, op := range members {
		current, e := s.DAL.Operations().Find(ctx, op.ID)
		if e != nil || current.Status != operations.StatusPending || current.Claim.Token != op.Claim.Token {
			continue
		}
		if err == nil {
			if e = s.write(ctx, func(t *Service) error { return t.DAL.Operations().Release(ctx, op.ID, op.Claim, time.Now()) }); e != nil && !errors.Is(e, operations.ErrClaimLost) {
				return true, e
			}
			continue
		}
		if e = s.endAttempt(ctx, op, true, err); e != nil && !errors.Is(e, err) {
			return true, e
		}
	}
	return true, err
}

// failClaimed ends a claimed operation with a failure code before it runs.
func (s *Service) failClaimed(ctx context.Context, handler OperationHandler, op *operations.Operation, code operations.ErrorCode, cause error) error {
	completed := time.Now().UTC()
	op.Status, op.ErrorCode, op.Error, op.CompletedAt = operations.StatusFailed, code, cause.Error(), &completed
	if err := s.SaveOperation(ctx, op); err != nil || handler.Failed == nil {
		return err
	}
	return handler.Failed(ctx, s, op, cause)
}
