package core

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"time"
	"unsafe"

	"github.com/cespare/xxhash/v2"
	"github.com/google/uuid"
)

const defaultLetters = "abcdefghijkmnpqrstuvwxyz0123456789"

func NewID(prefix string) string {
	// prefix最多16位
	if len(prefix) > 16 {
		prefix = prefix[0:16]
	}
	return fmt.Sprintf("%s-%s", prefix, RandomString(8))
}

// RandomString returns a random string with a fixed length
func RandomString(n int, allowedChars ...string) string {
	var letters string

	if len(allowedChars) == 0 {
		letters = defaultLetters
	} else {
		letters = allowedChars[0]
	}

	b := make([]byte, n)
	for i := range b {
		rand.Seed(time.Now().UnixNano())
		b[i] = letters[rand.Intn(len(letters))]
	}

	return *(*string)(unsafe.Pointer(&b))
}

func NewHashID(raw []byte) string {
	hash64 := xxhash.Sum64(raw)
	id := fmt.Sprintf("%x", hash64)
	return id
}

func NewHashIDByObject(obj any) (string, error) {
	raw, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}

	id := NewHashID(raw)
	return id, nil
}

func NewUUID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}
