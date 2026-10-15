package sync

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// NewKey creates a 256-bit key encoded as unpadded base64url.
func NewKey() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", errors.New("could not create encryption key")
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeKey(key string) ([]byte, error) {
	data, err := base64.RawURLEncoding.Strict().DecodeString(key)
	if err != nil || len(data) != 32 {
		return nil, errors.New("invalid encryption key")
	}
	return data, nil
}

func encryptionCipher(key string) (cipher.AEAD, error) {
	data, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(data)
	if err != nil {
		return nil, errors.New("could not initialize encryption")
	}
	return cipher.NewGCM(block)
}

// Seal compresses JSON and encrypts it in the version-one authenticated envelope.
func Seal(plaintext []byte, key, aad string) ([]byte, error) {
	encryption, err := encryptionCipher(key)
	if err != nil {
		return nil, err
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err = writer.Write(plaintext); err != nil {
		return nil, err
	}
	if err = writer.Close(); err != nil {
		return nil, err
	}
	envelope := make([]byte, 13)
	envelope[0] = 1
	if _, err = rand.Read(envelope[1:]); err != nil {
		return nil, errors.New("could not create encryption nonce")
	}
	return encryption.Seal(envelope, envelope[1:], compressed.Bytes(), []byte(aad)), nil
}

// Open authenticates and decompresses a version-one envelope with a bounded output.
func Open(envelope []byte, key, aad string) ([]byte, error) {
	if len(envelope) < 29 || envelope[0] != 1 {
		return nil, errors.New("invalid encrypted envelope")
	}
	encryption, err := encryptionCipher(key)
	if err != nil {
		return nil, err
	}
	compressed, err := encryption.Open(nil, envelope[1:13], envelope[13:], []byte(aad))
	if err != nil {
		return nil, errors.New("could not authenticate encrypted envelope")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errors.New("invalid compressed envelope")
	}
	defer reader.Close()
	plaintext, err := io.ReadAll(io.LimitReader(reader, (32<<20)+1))
	if err != nil || len(plaintext) > 32<<20 {
		return nil, errors.New("invalid or oversized compressed envelope")
	}
	return plaintext, nil
}
