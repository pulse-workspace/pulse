package user

import (
	"github.com/vmihailenco/msgpack/v5"
)

// msgpackMarshal marshals v using Pulse's msgpack convention.
func msgpackMarshal(v any) ([]byte, error) {
	return msgpack.Marshal(v)
}

// mustMarshal marshals or panics — only for static literals.
func mustMarshal(v any) []byte {
	b, err := msgpackMarshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
