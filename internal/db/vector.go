package db

import (
	"encoding/binary"
	"math"
	"sort"
)

// encodeVector 把 float32 向量編碼成 little-endian BLOB，供 SQLite 儲存。
func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector 把 BLOB 還原成 float32 向量。
func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// cosineSimilarity 計算兩個向量的餘弦相似度；任一向量為零向量時回傳 0。
// 若兩向量長度不同（例如換嵌入模型後、reindex 尚未完成時新舊維度的向量
// 同時存在），視為不相似並回傳 0，而非索引越界 panic。
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// reciprocalRankFusion 合併兩組依名次排序的 ID 清單（各自第一名代表最相關），
// 用 RRF 公式 score = Σ 1/(rrfK + rank) 計算融合分數，回傳依分數由高到低排序
// 的 ID 清單。rrfK 是平滑常數（業界慣用值 60）。分數相同時保留 a 優先、原始
// 加入順序在後（stable sort）。
func reciprocalRankFusion(a, b []int64, rrfK float64) []int64 {
	scores := make(map[int64]float64)
	var order []int64

	addRanks := func(ids []int64) {
		for i, id := range ids {
			if _, seen := scores[id]; !seen {
				order = append(order, id)
			}
			scores[id] += 1.0 / (rrfK + float64(i+1))
		}
	}
	addRanks(a)
	addRanks(b)

	sort.SliceStable(order, func(i, j int) bool {
		return scores[order[i]] > scores[order[j]]
	})
	return order
}
