package onnx

import (
	"context"
	"os"
	"slices"
	"testing"
)

// TestNativeABI runs a model on the ONNX Runtime library in ONNXRUNTIME_LIB.
// Without it the test is skipped, unless PAPERGO_REQUIRE_OCR is set (as in
// CI). The model negates a float input and casts an int64 input to float,
// so it reads metadata, takes both input tensor types and returns outputs.
func TestNativeABI(t *testing.T) {
	library := os.Getenv("ONNXRUNTIME_LIB")
	if library == "" {
		if os.Getenv("PAPERGO_REQUIRE_OCR") != "" {
			t.Fatal("PAPERGO_REQUIRE_OCR is set but ONNXRUNTIME_LIB is not")
		}
		t.Skip("set ONNXRUNTIME_LIB to the ONNX Runtime library to run native tests")
	}
	r, err := Load(library)
	if err != nil {
		t.Fatal(err)
	}
	probe(t, r)
}

// probe runs the probe model on r and checks its metadata and outputs.
func probe(t *testing.T, r *Runtime) {
	t.Helper()
	s, err := r.NewSession(probeModel(), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !slices.Equal(s.Inputs, []string{"x", "n"}) || !slices.Equal(s.Outputs, []string{"y", "z"}) ||
		!slices.Equal(s.InputTypes, []string{"float32", "int64"}) || !slices.Equal(s.OutputTypes, []string{"float32", "float32"}) ||
		!slices.Equal(s.InputShapes[0], []int64{-1, 3}) {
		t.Fatalf("model metadata: %+v", s)
	}
	out, err := s.Run(context.Background(), []Tensor{
		{Shape: []int64{2, 3}, Float32: []float32{1, -2, 3, 0, 5, -6}},
		{Shape: []int64{2, 3}, Int64: []int64{7, 8, 9, -1, 0, 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out[0].Shape, []int64{2, 3}) || !slices.Equal(out[0].Float32, []float32{-1, 2, -3, 0, -5, 6}) ||
		!slices.Equal(out[1].Float32, []float32{7, 8, 9, -1, 0, 1}) {
		t.Fatalf("model outputs: %+v", out)
	}
}

// probeModel encodes the ONNX ModelProto of y = Neg(x), z = Cast(n, float)
// with x and n of shape [batch, 3], at IR version 8 and opset 13.
func probeModel() []byte {
	const float, int64Type = 1, 7
	tensor := func(name string, elem uint64) []byte {
		shape := cat(bytesField(1, bytesField(2, []byte("batch"))), bytesField(1, varintField(1, 3)))
		return cat(bytesField(1, []byte(name)), bytesField(2, bytesField(1, cat(varintField(1, elem), bytesField(2, shape)))))
	}
	node := func(op, input, output string, attributes ...[]byte) []byte {
		return cat(bytesField(1, []byte(input)), bytesField(2, []byte(output)), bytesField(4, []byte(op)), cat(attributes...))
	}
	to := bytesField(5, cat(bytesField(1, []byte("to")), varintField(3, float), varintField(20, 2)))
	graph := cat(
		bytesField(1, node("Neg", "x", "y")),
		bytesField(1, node("Cast", "n", "z", to)),
		bytesField(2, []byte("probe")),
		bytesField(11, tensor("x", float)), bytesField(11, tensor("n", int64Type)),
		bytesField(12, tensor("y", float)), bytesField(12, tensor("z", float)),
	)
	return cat(varintField(1, 8), bytesField(7, graph), bytesField(8, varintField(2, 13)))
}

func varint(v uint64) []byte {
	var out []byte
	for v >= 0x80 {
		out = append(out, byte(v)|0x80)
		v >>= 7
	}
	return append(out, byte(v))
}

func varintField(number int, v uint64) []byte {
	return append(varint(uint64(number)<<3), varint(v)...)
}

func bytesField(number int, payload ...[]byte) []byte {
	data := cat(payload...)
	return cat(varint(uint64(number)<<3|2), varint(uint64(len(data))), data)
}

func cat(parts ...[]byte) []byte {
	return slices.Concat(parts...)
}
