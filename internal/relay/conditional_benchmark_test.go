package relay

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type benchmarkResponse struct {
	header http.Header
	status int
	bytes  int
}

func (response *benchmarkResponse) Header() http.Header { return response.header }

func (response *benchmarkResponse) WriteHeader(status int) { response.status = status }

func (response *benchmarkResponse) Write(data []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	response.bytes += len(data)
	return len(data), nil
}

// BenchmarkSpaceConditional measures validators independently of response-body buffering.
func BenchmarkSpaceConditional(benchmark *testing.B) {
	for _, size := range []int{1 << 10, 1 << 20, 4 << 20} {
		for _, isMatched := range []bool{true, false} {
			benchmark.Run(fmt.Sprintf("bytes_%d/matched_%t", size, isMatched), func(benchmark *testing.B) {
				server, err := New(Config{Token: testToken, DataDir: benchmark.TempDir()})
				if err != nil {
					benchmark.Fatal(err)
				}
				benchmark.Cleanup(func() { _ = server.Close() })
				data := bytes.Repeat([]byte{1}, size)
				if err := server.store.replaceSpace(benchmark.Context(), testID, data, time.Now()); err != nil {
					benchmark.Fatal(err)
				}
				path := "/api/spaces/" + testID
				initial := performRequest(server, http.MethodGet, path, "", nil)
				condition := initial.Header().Get("ETag")
				status, bodySize := http.StatusNotModified, 0
				if !isMatched {
					condition = `"unmatched"`
					status, bodySize = http.StatusOK, size
				}
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("If-None-Match", condition)
				benchmark.ReportAllocs()
				benchmark.ResetTimer()
				for range benchmark.N {
					response := &benchmarkResponse{header: make(http.Header)}
					server.ServeHTTP(response, request)
					if response.status != status || response.bytes != bodySize {
						benchmark.Fatalf("response = %d/%d bytes; want %d/%d", response.status, response.bytes, status, bodySize)
					}
				}
			})
		}
	}
}
