package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCompleteJSONStripsFences(t *testing.T) {
	var got Request
	c := FuncClient{ID: "fake", Fn: func(_ context.Context, req Request) (string, error) {
		got = req
		return "```json\n{\"n\": 3}\n```\n", nil
	}}
	var out struct{ N int }
	if err := CompleteJSON(context.Background(), c, Request{Prompt: "p"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.N != 3 {
		t.Errorf("out = %+v", out)
	}
	if !strings.Contains(got.System, "JSON") {
		t.Errorf("system should ask for JSON only, got %q", got.System)
	}
}

func TestCompleteJSONRetriesOnceWithParseError(t *testing.T) {
	var prompts []string
	c := FuncClient{ID: "fake", Fn: func(_ context.Context, req Request) (string, error) {
		prompts = append(prompts, req.Prompt)
		if len(prompts) == 1 {
			return "not json", nil
		}
		return `{"ok":true}`, nil
	}}
	var out struct{ OK bool }
	if err := CompleteJSON(context.Background(), c, Request{Prompt: "原始"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || !out.OK {
		t.Fatalf("calls = %d, out = %+v", len(prompts), out)
	}
	if !strings.HasPrefix(prompts[1], "原始") || !strings.Contains(prompts[1], "invalid character") {
		t.Errorf("retry prompt should append the parse error: %q", prompts[1])
	}
}

func TestCompleteJSONGivesUpAfterOneRetry(t *testing.T) {
	calls := 0
	c := FuncClient{ID: "fake", Fn: func(context.Context, Request) (string, error) {
		calls++
		return "still not json", nil
	}}
	var out map[string]any
	if err := CompleteJSON(context.Background(), c, Request{}, &out); err == nil {
		t.Fatal("want error")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

func TestCompleteJSONPropagatesClientError(t *testing.T) {
	calls := 0
	c := FuncClient{ID: "fake", Fn: func(context.Context, Request) (string, error) {
		calls++
		return "", errors.New("down")
	}}
	var out map[string]any
	if err := CompleteJSON(context.Background(), c, Request{}, &out); err == nil || calls != 1 {
		t.Errorf("err = %v calls = %d, want error without retry", err, calls)
	}
}

func TestWithTierTimeout(t *testing.T) {
	for _, tc := range []struct {
		tier Tier
		want time.Duration
	}{{TierCheap, 20 * time.Second}, {TierSynth, 90 * time.Second}} {
		ctx, cancel := withTierTimeout(context.Background(), tc.tier)
		dl, ok := ctx.Deadline()
		cancel()
		if !ok || time.Until(dl) > tc.want || time.Until(dl) < tc.want-time.Second {
			t.Errorf("tier %v: deadline in %v, want ~%v", tc.tier, time.Until(dl), tc.want)
		}
	}
	parent, cancelParent := context.WithTimeout(context.Background(), time.Hour)
	defer cancelParent()
	ctx, cancel := withTierTimeout(parent, TierCheap)
	defer cancel()
	if dl, _ := ctx.Deadline(); time.Until(dl) < 59*time.Minute {
		t.Errorf("existing deadline must be kept, got %v", time.Until(dl))
	}
}

type memCounter struct {
	n   map[string]int
	err error
}

func (m *memCounter) Incr(day string) (int, error) {
	if m.err != nil {
		return 0, m.err
	}
	m.n[day]++
	return m.n[day], nil
}

func TestWithBudget(t *testing.T) {
	calls := 0
	inner := FuncClient{ID: "fake/x", Fn: func(context.Context, Request) (string, error) {
		calls++
		return "ok", nil
	}}
	c := WithBudget(inner, &memCounter{n: map[string]int{}}, 2)
	for i := 0; i < 2; i++ {
		if _, err := c.Complete(context.Background(), Request{}); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	if _, err := c.Complete(context.Background(), Request{}); !errors.Is(err, ErrBudget) {
		t.Errorf("third call err = %v, want ErrBudget", err)
	}
	if calls != 2 {
		t.Errorf("inner calls = %d, want 2", calls)
	}
	if c.Name() != "fake/x" {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestWithBudgetUnlimitedAndCounterError(t *testing.T) {
	inner := FuncClient{ID: "fake", Fn: func(context.Context, Request) (string, error) { return "ok", nil }}
	if c := WithBudget(inner, nil, 0); c.Name() != "fake" {
		t.Errorf("limit 0 should return the client unchanged")
	}
	c := WithBudget(inner, &memCounter{err: errors.New("db locked")}, 5)
	if _, err := c.Complete(context.Background(), Request{}); err == nil || !strings.Contains(err.Error(), "db locked") {
		t.Errorf("err = %v, want counter error", err)
	}
}

func TestModelForThroughBudget(t *testing.T) {
	a := &Anthropic{CheapModel: "c", SynthModel: "s"}
	c := WithBudget(a, &memCounter{n: map[string]int{}}, 3)
	if ModelFor(c, TierCheap) != "c" || ModelFor(c, TierSynth) != "s" {
		t.Errorf("ModelFor = %q / %q", ModelFor(c, TierCheap), ModelFor(c, TierSynth))
	}
	if ModelFor(FuncClient{}, TierCheap) != "" {
		t.Error("client without models should return empty")
	}
}
