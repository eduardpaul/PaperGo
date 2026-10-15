package onnx

import "testing"

func TestTensorElements(t *testing.T) {
	for _, test := range []struct {
		shape []int64
		want  int
	}{{nil, 1}, {[]int64{2, 3}, 6}, {[]int64{0, 512}, 0}} {
		got, err := elements(test.shape)
		if err != nil || got != test.want {
			t.Errorf("elements(%v)=%d,%v; want %d", test.shape, got, err, test.want)
		}
	}
	for _, shape := range [][]int64{{-1, 2}, {int64(^uint(0) >> 1), 2}} {
		if _, err := elements(shape); err == nil {
			t.Errorf("invalid shape %v accepted", shape)
		}
	}
}
