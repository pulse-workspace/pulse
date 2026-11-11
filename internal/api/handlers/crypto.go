package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argonParams is the Argon2id parameter set.
type argonParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
	saltLength  uint32
	keyLength   uint32
}

var defaultArgon = argonParams{
	memory:      64 * 1024,
	iterations:  3,
	parallelism: 4,
	saltLength:  16,
	keyLength:   32,
}

// hashPassword returns a PHC-formatted Argon2id hash of plain.
func hashPassword(plain string) (string, error) {
	salt := make([]byte, defaultArgon.saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(plain), salt,
		defaultArgon.iterations, defaultArgon.memory,
		defaultArgon.parallelism, defaultArgon.keyLength)
	return encodeHash(defaultArgon, salt, key), nil
}

// verifyPassword reports whether plain matches the PHC-formatted encoded hash.
func verifyPassword(plain, encoded string) bool {
	p, salt, key, err := decodeHash(encoded)
	if err != nil {
		return false
	}
	want := argon2.IDKey([]byte(plain), salt, p.iterations, p.memory, p.parallelism, p.keyLength)
	return subtle.ConstantTimeCompare(want, key) == 1
}

func encodeHash(p argonParams, salt, key []byte) string {
	return "$argon2id$v=19$m=" + strconv.FormatUint(uint64(p.memory), 10) +
		",t=" + strconv.FormatUint(uint64(p.iterations), 10) +
		",p=" + strconv.FormatUint(uint64(p.parallelism), 10) +
		"$" + base64.RawStdEncoding.EncodeToString(salt) +
		"$" + base64.RawStdEncoding.EncodeToString(key)
}

func decodeHash(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return argonParams{}, nil, nil, fmt.Errorf("invalid hash format")
	}
	if parts[1] != "argon2id" {
		return argonParams{}, nil, nil, fmt.Errorf("unsupported algorithm")
	}
	var p argonParams
	for _, kv := range strings.Split(parts[3], ",") {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return argonParams{}, nil, nil, fmt.Errorf("invalid param")
		}
		switch k {
		case "m":
			n, _ := strconv.ParseUint(v, 10, 32)
			p.memory = uint32(n)
		case "t":
			n, _ := strconv.ParseUint(v, 10, 32)
			p.iterations = uint32(n)
		case "p":
			n, _ := strconv.ParseUint(v, 10, 8)
			p.parallelism = uint8(n)
		}
	}
	p.saltLength = 16
	p.keyLength = 32
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, err
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, err
	}
	return p, salt, key, nil
}
