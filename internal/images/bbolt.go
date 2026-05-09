// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package images

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/torrplay/torrplay/internal/httpclient"
	"go.etcd.io/bbolt"
)

const (
	imagesBucket = "images"
	// 1MB limit for image size.
	maxImageSize = 1 * 1024 * 1024
)

var _ ServiceInterface = (*BBoltDBService)(nil)

var (
	ErrImageNotFound = errors.New("image not found")
	ErrImageTooLarge = errors.New("image too large")
)

var ImageTypes = map[string]string{
	"image/apng":    ".apng",
	"image/avif":    ".avif",
	"image/bmp":     ".bmp",
	"image/gif":     ".gif",
	"image/jpeg":    ".jpeg",
	"image/png":     ".png",
	"image/svg+xml": ".svg",
	"image/webp":    ".webp",
}

type BBoltDBService struct {
	db         *bbolt.DB
	httpClient *httpclient.Client
}

// NewBBoltDBService creates a new Service with a default HTTP client.
func NewBBoltDBService(path string) (*BBoltDBService, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	return NewServiceWithClient(path, httpclient.New(httpclient.WithJar(jar)))
}

// NewServiceWithClient creates a new Service with a custom HTTP client.
func NewServiceWithClient(path string, client *httpclient.Client) (*BBoltDBService, error) {
	db, err := bbolt.Open(path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(imagesBucket))
		if err != nil {
			return fmt.Errorf("failed to create images bucket: %v", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &BBoltDBService{
		db:         db,
		httpClient: client,
	}, nil
}

func (s *BBoltDBService) Close() error {
	return s.db.Close()
}

func (s *BBoltDBService) Delete(id *string) error {
	if id == nil {
		return nil
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		return bucket.Delete([]byte(*id))
	})
}

func (s *BBoltDBService) DownloadImageData(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("URL is required")
	}

	if strings.HasPrefix(url, "data:image") {
		// Handle data URI
		parts := strings.Split(url, ",")
		if len(parts) != 2 {
			return nil, errors.New("invalid data URI")
		}

		return base64.StdEncoding.DecodeString(parts[1])
	}

	resp, err := s.httpClient.Get(ctx, url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d from %s", resp.StatusCode, url)
	}

	// Read image data with limit.
	var buf bytes.Buffer
	_, err = io.Copy(&buf, io.LimitReader(resp.Body, int64(maxImageSize+1)))
	if err != nil {
		return nil, err
	}

	if buf.Len() > maxImageSize {
		return nil, ErrImageTooLarge
	}

	data := buf.Bytes()

	// Validate image type.
	contentType := http.DetectContentType(data)
	if !isImageContentType(contentType) {
		return nil, fmt.Errorf("unsupported image type: %s", contentType)
	}

	return data, nil
}

func (s *BBoltDBService) Get(id string) ([]byte, error) {
	var data []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		bytes := bucket.Get([]byte(id))
		if bytes == nil {
			return ErrImageNotFound
		}
		data = make([]byte, len(bytes))
		copy(data, bytes)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (s *BBoltDBService) ListIDs() ([]string, error) {
	var ids []string
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		return bucket.ForEach(func(k, v []byte) error {
			ids = append(ids, string(k))
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return ids, nil
}

func (s *BBoltDBService) SaveData(data []byte) (*string, error) {
	if len(data) == 0 {
		return nil, errors.New("empty data")
	}

	// Generate ID from data hash.
	hash := sha256.Sum256(data)
	id := hex.EncodeToString(hash[:])

	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))

		existing := bucket.Get([]byte(id))
		if existing != nil {
			return nil
		}

		return bucket.Put([]byte(id), data)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to save data: %v", err)
	}

	return &id, nil
}

func (s *BBoltDBService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/")

	data, err := s.Get(id)
	if err != nil {
		st := http.StatusInternalServerError
		if errors.Is(err, ErrImageNotFound) {
			st = http.StatusNotFound
		}
		http.Error(w, err.Error(), st)
		return
	}

	contentType := http.DetectContentType(data)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

// isImageContentType checks if content type is an image.
func isImageContentType(contentType string) bool {
	_, ok := ImageTypes[strings.ToLower(contentType)]
	return ok
}
