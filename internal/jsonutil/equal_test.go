package jsonutil

import "testing"

func TestEqual(t *testing.T) {
	for _, test := range []struct {
		a, b  string
		equal bool
	}{
		{`{"a":1,"b":[true,null,"x"]}`, `{ "b": [true,null,"x"], "a":1.0e0}`, true},
		{`9007199254740993`, `9007199254740992`, false},
		{`9007199254740993`, `9007199254740993.00`, true},
		{`-0`, `0.0e99999999999999999999999`, true},
		{`1e99999999999999999999`, `10e99999999999999999998`, true},
		{`0.01230`, `123e-4`, true},
		{`-1.23`, `1.23`, false},
		{`{"a":null}`, `{"b":null}`, false},
		{`null`, `{}`, false},
		{`1 2`, `1`, false},
		{`invalid`, `null`, false},
		{`[1,2]`, `[2,1]`, false},
		{`1`, `"1"`, false},
	} {
		if got := Equal([]byte(test.a), []byte(test.b)); got != test.equal {
			t.Errorf("Equal(%s,%s)=%v", test.a, test.b, got)
		}
	}
}
