// Package aiops runs AI work as operations through the API:
//
//   - POST /v1/ai/completions asks the configured chat model to do something
//     with text or an image. Immediate completions are ai.complete
//     operations; batch completions are ai.batch operations, which their
//     worker admits in groups per queue and sends as provider batches.
//   - POST /v1/ai/decisions answers choice, noul and score questions with
//     the local decision engine, and POST /v1/ai/taxonomy-decisions picks
//     managed terms of a term set for a document (ai.decide and
//     taxonomy.decide).
//
// Answers are cached: the same model, prompt, schema and input reuse the
// answer.
package aiops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"papergo/internal/ai"
	"papergo/internal/dal/activitystore"
	"papergo/internal/dal/operations"
	"papergo/internal/dal/resultcache"
	"papergo/internal/decision"
	"papergo/internal/dms"
	"papergo/internal/storage"
)

// Operation kinds.
const (
	KindComplete = "ai.complete"
	KindBatch    = "ai.batch"
	KindDecide   = "ai.decide"
	KindTaxonomy = "taxonomy.decide"
)

// Assistant runs AI operations with the deployment's models. A nil model
// refuses the requests that need it.
type Assistant struct {
	Storage   storage.Store
	Chat      ai.Chat
	Batch     ai.Batcher
	Decisions decision.Model
	// Results caches answers for ResultTTL after their last use.
	Results   resultcache.Cache
	ResultTTL time.Duration
	// Records keeps provider batches in flight.
	Records *activitystore.Store
	// DefaultExecution is immediate or batch, for completions that do not
	// choose (AI_EXECUTION).
	DefaultExecution string
	// BatchDeadline bounds how long a batch completion may wait; default
	// 72 hours.
	BatchDeadline time.Duration

	svc *dms.Service
}

var (
	// ErrNoChat refuses immediate completions without a chat model.
	ErrNoChat = errors.New("AI completions need a chat model; configure AI_CHAT_PROVIDER")
	// ErrNoBatch refuses batch completions without a batch provider.
	ErrNoBatch = errors.New("batch completions need a batch provider; configure AI_BATCH_PROVIDER")
	// ErrNoDecisions refuses decisions without the local decision engine.
	ErrNoDecisions = errors.New("decisions need AI_DECISION_MODELS_PATH and ONNXRUNTIME_LIB")
)

const (
	answerScope       = "installation"
	maxPromptChars    = 8000
	defaultInputChars = 20_000
	maxInputChars     = 200_000
	defaultDeadline   = 72 * time.Hour
	defaultQueue      = "shared"
	// maxAnswer bounds an answer, which is an operation result.
	maxAnswer = 256 << 10
)

// Register makes s run the operations. Requests that need a model the
// deployment lacks are refused.
func (a *Assistant) Register(s *dms.Service) error {
	a.svc = s
	deadline := a.BatchDeadline
	if deadline <= 0 {
		deadline = defaultDeadline
	}
	handlers := map[string]dms.OperationHandler{
		KindComplete: {Scope: "document.read", Validate: a.validateCompletion, Execute: a.complete},
		KindBatch:    {Scope: "document.read", Validate: a.validateCompletion, Deadline: deadline, Admission: batchAdmission, ExecuteGroup: a.submit},
		KindDecide:   {Scope: "document.read", Validate: a.validateDecision, Execute: a.decide},
		KindTaxonomy: {Scope: "document.read", Validate: a.validateDecision, Execute: a.decide},
	}
	for kind, h := range handlers {
		if err := s.RegisterOperationHandler(kind, h); err != nil {
			return err
		}
	}
	return nil
}

func (a *Assistant) cacheReady() error {
	if a.Results == nil || a.ResultTTL <= 0 {
		return errors.New("AI operations need a result cache with a positive TTL")
	}
	return nil
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func shortText(text string, limit int) string {
	if r := []rune(text); len(r) > limit {
		return string(r[:limit])
	}
	return text
}

func (a *Assistant) readObject(ctx context.Context, key string) ([]byte, error) {
	f, err := a.Storage.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	const limit = 20 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if len(data) > limit {
		return nil, errors.New("the AI input exceeds 20 MiB")
	}
	return data, err
}

// succeed ends op with result, under its claim.
func succeed(ctx context.Context, s *dms.Service, op *operations.Operation, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(t *dms.Service) error {
		completed := time.Now().UTC()
		op.Status, op.Result, op.CompletedAt = operations.StatusSucceeded, raw, &completed
		return t.SaveOperation(ctx, op)
	})
}
