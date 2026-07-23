package db

import (
	"math"
	"testing"
)

func TestEncodeDecodeVector(t *testing.T) {
	v := []float32{0.1, -0.2, 3.5, 0}
	blob := encodeVector(v)
	if len(blob) != len(v)*4 {
		t.Fatalf("len(blob) = %d, want %d", len(blob), len(v)*4)
	}
	got := decodeVector(blob)
	if len(got) != len(v) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(v))
	}
	for i := range v {
		if got[i] != v[i] {
			t.Fatalf("got[%d] = %f, want %f", i, got[i], v[i])
		}
	}
}

func TestCosineSimilarityIdentical(t *testing.T) {
	v := []float32{1, 2, 3}
	got := cosineSimilarity(v, v)
	if math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("cosineSimilarity(v, v) = %f, want 1.0", got)
	}
}

func TestCosineSimilarityOrthogonal(t *testing.T) {
	a := []float32{1, 0}
	b := []float32{0, 1}
	got := cosineSimilarity(a, b)
	if math.Abs(got) > 1e-9 {
		t.Fatalf("cosineSimilarity(orthogonal) = %f, want 0", got)
	}
}

func TestCosineSimilarityZeroVector(t *testing.T) {
	a := []float32{0, 0}
	b := []float32{1, 1}
	if got := cosineSimilarity(a, b); got != 0 {
		t.Fatalf("cosineSimilarity(zero vector) = %f, want 0", got)
	}
}

func TestReciprocalRankFusionOverlap(t *testing.T) {
	// id 1 在兩個排名都名列前茅，融合後應該排第一。
	a := []int64{1, 2, 3}
	b := []int64{1, 3, 2}
	got := reciprocalRankFusion(a, b, 60)
	if got[0] != 1 {
		t.Fatalf("got[0] = %d, want 1", got[0])
	}
}

func TestReciprocalRankFusionOneEmpty(t *testing.T) {
	a := []int64{5, 6}
	got := reciprocalRankFusion(a, nil, 60)
	if len(got) != 2 || got[0] != 5 || got[1] != 6 {
		t.Fatalf("got = %v, want [5 6]", got)
	}
}

func TestReciprocalRankFusionDisjoint(t *testing.T) {
	a := []int64{1, 2}
	b := []int64{3, 4}
	got := reciprocalRankFusion(a, b, 60)
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	// a、b 的第一名分數並列最高；stable sort 保留 a 先加入的順序，1 排最前。
	if got[0] != 1 {
		t.Fatalf("got[0] = %d, want 1", got[0])
	}
}
