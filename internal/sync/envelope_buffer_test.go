package sync

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"math/rand/v2"
	"testing"
)

// TestSealPreservesCompressedWireBytes checks the envelope with an independent
// decryptor across buffer growth boundaries and both compression extremes.
func TestSealPreservesCompressedWireBytes(test *testing.T) {
	keyBytes := bytes.Repeat([]byte{7}, 32)
	key := base64.RawURLEncoding.EncodeToString(keyBytes)
	for _, size := range []int{0, 1, 31, 32, 64, 128, 1024, 64 << 10, 1 << 20} {
		for _, isVaried := range []bool{false, true} {
			test.Run(fmt.Sprintf("bytes_%d/varied_%t", size, isVaried), func(test *testing.T) {
				test.Parallel()
				plaintext := bytes.Repeat([]byte{'a'}, size)
				if isVaried {
					random := rand.New(rand.NewPCG(42, 67))
					for index := range plaintext {
						plaintext[index] = byte(random.Uint32())
					}
				}
				original := bytes.Clone(plaintext)
				var expected bytes.Buffer
				writer := gzip.NewWriter(&expected)
				if _, err := writer.Write(original); err != nil {
					test.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					test.Fatal(err)
				}
				envelope, err := Seal(plaintext, key, "space:buffer-contract")
				if err != nil {
					test.Fatal(err)
				}
				if len(envelope) != 13+expected.Len()+16 || envelope[0] != 1 {
					test.Fatal("version, header, or authentication tag changed")
				}
				block, err := aes.NewCipher(keyBytes)
				if err != nil {
					test.Fatal(err)
				}
				decryptor, err := cipher.NewGCM(block)
				if err != nil {
					test.Fatal(err)
				}
				compressed, err := decryptor.Open(nil, envelope[1:13], envelope[13:], []byte("space:buffer-contract"))
				if err != nil || !bytes.Equal(compressed, expected.Bytes()) {
					test.Fatal("authenticated compression bytes changed", err)
				}
				opened, err := Open(envelope, key, "space:buffer-contract")
				if err != nil || !bytes.Equal(opened, original) || !bytes.Equal(plaintext, original) {
					test.Fatal("seal changed input or round-trip plaintext", err)
				}
				previous := bytes.Clone(envelope)
				second, err := Seal(plaintext, key, "share:buffer-contract")
				if err != nil || !bytes.Equal(envelope, previous) {
					test.Fatal("later seal changed an earlier result", err)
				}
				second[len(second)-1] ^= 1
				if !bytes.Equal(envelope, previous) || !bytes.Equal(plaintext, original) {
					test.Fatal("envelopes share storage with another result or caller input")
				}
				if _, err := Open(second, key, "share:buffer-contract"); err == nil {
					test.Fatal("tampered envelope authenticated")
				}
			})
		}
	}
}
