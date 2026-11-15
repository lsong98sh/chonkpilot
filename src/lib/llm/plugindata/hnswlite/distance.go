package hnswlite

import "math"

// DistanceFunc is a function that computes the distance between two vectors.
type DistanceFunc func(a, b []float32) float32

// CosineDistance computes the cosine distance between two vectors（1 - 余弦相似度，范围 [0,2]）。
// 用 float64 累加，避免长向量 float32 累积误差；空向量 / 零向量 → 距离 1（正交语义）。
func CosineDistance(a, b []float32) float32 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		na += x * x
		nb += y * y
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return float32(1 - dot/(math.Sqrt(na)*math.Sqrt(nb)))
}

// EuclideanDistance computes the Euclidean distance between two vectors.
func EuclideanDistance(a, b []float32) float32 {
	var sum float64
	for i := range a {
		if i >= len(b) {
			break
		}
		diff := float64(a[i] - b[i])
		sum += diff * diff
	}
	return float32(math.Sqrt(sum))
}
