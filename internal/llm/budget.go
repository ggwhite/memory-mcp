package llm

import (
	"context"
	"fmt"
	"time"
)

// Counter 每日 LLM 呼叫計數器；Incr 把 day（YYYY-MM-DD）的次數加一並回傳加完後的值。
type Counter interface {
	Incr(day string) (int, error)
}

// WithBudget 包裝 c：每次呼叫前先用 counter 計數，當日次數超過 limit 就回傳 ErrBudget 且不呼叫 c。
// limit <= 0 表示不限制，直接回傳 c。
func WithBudget(c Client, counter Counter, limit int) Client {
	if limit <= 0 || counter == nil {
		return c
	}
	return &budgeted{inner: c, counter: counter, limit: limit}
}

type budgeted struct {
	inner   Client
	counter Counter
	limit   int
}

// Complete 見 Client。
func (b *budgeted) Complete(ctx context.Context, req Request) (string, error) {
	n, err := b.counter.Incr(time.Now().Format("2006-01-02"))
	if err != nil {
		return "", fmt.Errorf("llm budget: %w", err)
	}
	if n > b.limit {
		return "", fmt.Errorf("%w (%d/day)", ErrBudget, b.limit)
	}
	return b.inner.Complete(ctx, req)
}

// Name 見 Client。
func (b *budgeted) Name() string { return b.inner.Name() }

// Model 見 Modeler。
func (b *budgeted) Model(t Tier) string { return ModelFor(b.inner, t) }
