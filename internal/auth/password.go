package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonKeyLength   = 32
)

type Argon2id struct{}

func (Argon2id) Hash(password string) (string, error) {
	if password == "" || utf8.RuneCountInString(password) > 128 {
		return "", errors.New("password must contain between 1 and 128 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonIterations, argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func (Argon2id) Verify(encodedHash, password string) (bool, error) {
	if encodedHash == "" {
		// Keep the missing-account path close to the cost of a real Argon2id check.
		salt := []byte("admin-auth-dummy-salt")
		_ = argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
		return false, nil
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, errors.New("invalid Argon2id hash encoding")
	}
	var memory, iterations, parallelism uint32
	for _, parameter := range strings.Split(parts[3], ",") {
		key, value, ok := strings.Cut(parameter, "=")
		if !ok {
			return false, errors.New("invalid Argon2id parameters")
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return false, errors.New("invalid Argon2id parameters")
		}
		switch key {
		case "m":
			memory = uint32(parsed)
		case "t":
			iterations = uint32(parsed)
		case "p":
			parallelism = uint32(parsed)
		default:
			return false, errors.New("unsupported Argon2id parameter")
		}
	}
	// Bound attacker-controlled database values so corrupt hashes cannot exhaust memory.
	if memory != argonMemory || iterations != argonIterations || parallelism != argonParallelism {
		return false, errors.New("Argon2id parameters do not match service policy")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false, errors.New("invalid Argon2id salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < 16 || len(expected) > 64 {
		return false, errors.New("invalid Argon2id digest")
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, uint8(parallelism), uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}
