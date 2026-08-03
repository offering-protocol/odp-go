package jsonvalue

import (
	"bytes"
	"encoding/json"
)

func Depth(data []byte) int {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return 0
	}
	maximum := 1
	type entry struct {
		depth int
		value any
	}
	pending := []entry{{depth: 1, value: value}}
	for len(pending) != 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		maximum = max(maximum, current.depth)
		switch typed := current.value.(type) {
		case []any:
			for _, child := range typed {
				pending = append(pending, entry{depth: current.depth + 1, value: child})
			}
		case map[string]any:
			for _, child := range typed {
				pending = append(pending, entry{depth: current.depth + 1, value: child})
			}
		}
	}
	return maximum
}
