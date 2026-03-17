// minio-gateway: serves MinIO objects with CORS Allow-Origin: *
// Usage: set MINIO_ENDPOINT, MINIO_ACCESS_KEY, MINIO_SECRET_KEY (optional), then run.
// GET /{bucket}/{key} → stream object from MinIO with CORS headers.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const defaultPort = "8080"

func main() {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}
	accessKey := os.Getenv("MINIO_ACCESS_KEY")
	if accessKey == "" {
		accessKey = "minioadmin"
	}
	secretKey := os.Getenv("MINIO_SECRET_KEY")
	if secretKey == "" {
		secretKey = "minioadmin"
	}
	useSSL := os.Getenv("MINIO_USE_SSL") == "true" || os.Getenv("MINIO_USE_SSL") == "1"
	port := os.Getenv("GATEWAY_PORT")
	if port == "" {
		port = defaultPort
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		log.Fatalf("minio client: %v", err)
	}

	cors := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Expose-Headers", "Content-Length, Content-Range, Accept-Ranges")
			w.Header().Set("Access-Control-Max-Age", "86400")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			h.ServeHTTP(w, r)
		})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "" {
			http.Error(w, "GET /{bucket}/{key}", http.StatusBadRequest)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			http.Error(w, "path must be /{bucket}/{key}", http.StatusBadRequest)
			return
		}
		bucket, objectKey := parts[0], parts[1]

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		// Stat first so we have size for Content-Length / Range responses
		info, err := client.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{})
		if err != nil {
			log.Printf("StatObject %s/%s: %v", bucket, objectKey, err)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		objectSize := info.Size
		if objectSize < 0 {
			objectSize = 0
		}

		w.Header().Set("Accept-Ranges", "bytes")
		if info.ContentType != "" {
			w.Header().Set("Content-Type", info.ContentType)
		}

		// Parse Range header for HTTP byte serving (required by PMTiles viewer)
		var start, end int64
		hasRange := false
		if rangeH := r.Header.Get("Range"); rangeH != "" {
			if strings.HasPrefix(rangeH, "bytes=") {
				rangeH = strings.TrimPrefix(rangeH, "bytes=")
				rangeParts := strings.SplitN(rangeH, "-", 2)
				if len(rangeParts) == 2 {
					if rangeParts[0] != "" {
						if s, err := strconv.ParseInt(strings.TrimSpace(rangeParts[0]), 10, 64); err == nil && s >= 0 {
							start = s
							hasRange = true
						}
					} else {
						// suffix-byte-range: bytes=-500 means last 500 bytes
						if s, err := strconv.ParseInt(strings.TrimSpace(rangeParts[1]), 10, 64); err == nil && s > 0 {
							start = objectSize - s
							if start < 0 {
								start = 0
							}
							hasRange = true
						}
					}
					if hasRange && rangeParts[1] != "" {
						if e, err := strconv.ParseInt(strings.TrimSpace(rangeParts[1]), 10, 64); err == nil && e >= start {
							end = e
						} else {
							end = objectSize - 1
						}
					} else if hasRange {
						end = objectSize - 1
					}
				}
			}
		}

		if r.Method == http.MethodHead {
			if hasRange {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, objectSize))
				w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
				w.WriteHeader(http.StatusPartialContent)
			} else {
				w.Header().Set("Content-Length", strconv.FormatInt(objectSize, 10))
				w.WriteHeader(http.StatusOK)
			}
			return
		}

		opts := minio.GetObjectOptions{}
		if hasRange {
			opts.SetRange(start, end)
		}

		obj, err := client.GetObject(ctx, bucket, objectKey, opts)
		if err != nil {
			log.Printf("GetObject %s/%s: %v", bucket, objectKey, err)
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		defer obj.Close()

		if hasRange {
			// Buffer the range so Content-Length matches the body exactly (avoids ERR_CONTENT_LENGTH_MISMATCH)
			expected := end - start + 1
			var buf bytes.Buffer
			buf.Grow(int(expected))
			_, err = io.CopyN(&buf, obj, expected)
			if err != nil && err != io.EOF {
				log.Printf("stream %s/%s range: %v", bucket, objectKey, err)
				http.Error(w, "range read failed", http.StatusInternalServerError)
				return
			}
			actual := int64(buf.Len())
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, objectSize))
			w.Header().Set("Content-Length", strconv.FormatInt(actual, 10))
			w.WriteHeader(http.StatusPartialContent)
			if _, err := w.Write(buf.Bytes()); err != nil {
				log.Printf("stream %s/%s write: %v", bucket, objectKey, err)
			}
			return
		}

		w.Header().Set("Content-Length", strconv.FormatInt(objectSize, 10))
		n, err := io.Copy(w, obj)
		if err != nil {
			log.Printf("stream %s/%s: %v", bucket, objectKey, err)
			return
		}
		if n != objectSize {
			log.Printf("stream %s/%s: short read %d != %d", bucket, objectKey, n, objectSize)
		}
		_ = n
	})

	addr := ":" + port
	log.Printf("minio-gateway listening on %s (CORS *=*)", addr)
	if err := http.ListenAndServe(addr, cors(mux)); err != nil {
		log.Fatal(err)
	}
}
