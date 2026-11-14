package responsecrypto

import "testing"

// Benchmarks use synthetic bytes; no product images, credentials or network I/O.
// The nonce and authentication tag follow the production Seal path on every run.
func BenchmarkR24Seal(b *testing.B) {
	for _, sample := range []struct {
		name string
		size int
	}{
		{"64KiB", 64 << 10},
		{"512KiB", 512 << 10},
		{"1.4MiB", 1468006},
		{"5MiB", 5 << 20},
		{"10MiB", 10 << 20},
	} {
		b.Run(sample.name, func(b *testing.B) {
			key := Key{Material: RandomKey()}
			body := make([]byte, sample.size, sample.size+16)
			aad := HTTPAAD("public", "synthetic-context", "synthetic-key", "synthetic-request", "GET", "/api/v1/media/synthetic/file", 200, "image/jpeg", "identity")
			b.SetBytes(int64(sample.size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if _, _, err := Seal(key, body, aad); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
