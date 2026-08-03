package odp

import (
	"bytes"
	"encoding/json"
)

type Optional[Value any] struct {
	null    bool
	present bool
	value   Value
}

func Some[Value any](value Value) Optional[Value] {
	return Optional[Value]{present: true, value: value}
}

func Null[Value any]() Optional[Value] {
	return Optional[Value]{null: true, present: true}
}

func (optional Optional[Value]) Get() (Value, bool) {
	return optional.value, optional.present && !optional.null
}

func (optional Optional[Value]) IsNull() bool {
	return optional.present && optional.null
}

func (optional Optional[Value]) IsPresent() bool {
	return optional.present
}

func (optional Optional[Value]) IsZero() bool {
	return !optional.present
}

func (optional Optional[Value]) MarshalJSON() ([]byte, error) {
	if optional.null || !optional.present {
		return []byte("null"), nil
	}
	return json.Marshal(optional.value)
}

func (optional *Optional[Value]) UnmarshalJSON(data []byte) error {
	optional.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		optional.null = true
		var zero Value
		optional.value = zero
		return nil
	}
	optional.null = false
	return json.Unmarshal(data, &optional.value)
}
