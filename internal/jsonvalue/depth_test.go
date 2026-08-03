package jsonvalue

import "testing"

func TestDepth(t *testing.T) {
	tests := []struct {
		data string
		want int
	}{
		{data: `null`, want: 1},
		{data: `{}`, want: 1},
		{data: `{"a":[{"b":true}]}`, want: 4},
		{data: `{`, want: 0},
	}
	for _, test := range tests {
		if got := Depth([]byte(test.data)); got != test.want {
			t.Fatalf("Depth(%q) = %d, want %d", test.data, got, test.want)
		}
	}
}
