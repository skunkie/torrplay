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
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
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

// DetectContentType detects the MIME type of the given image data.
// In addition to standard formats supported by http.DetectContentType,
// it recognizes AVIF, BMP, and SVG.
func DetectContentType(data []byte) string {
	if len(data) >= 14 && data[0] == 'B' && data[1] == 'M' {
		return "image/bmp"
	}
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		brand := string(data[8:12])
		if brand == "avif" || brand == "avis" {
			return "image/avif"
		}
	}
	if isSVG(data) {
		return "image/svg+xml"
	}

	return http.DetectContentType(data)
}

func isSVG(data []byte) bool {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	rootSeen := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return rootSeen && depth == 0
		}
		if err != nil {
			return false
		}
		switch element := token.(type) {
		case xml.StartElement:
			if depth == 0 {
				if rootSeen || element.Name.Local != "svg" ||
					(element.Name.Space != "" && element.Name.Space != "http://www.w3.org/2000/svg") {
					return false
				}
				rootSeen = true
			}
			depth++
		case xml.EndElement:
			depth--
			if depth < 0 {
				return false
			}
		}
	}
}

// IsImageContentType checks if content type is a supported image MIME type.
func IsImageContentType(contentType string) bool {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	_, ok := ImageTypes[strings.ToLower(mediaType)]
	return ok
}

type BBoltDBService struct {
	db         *bbolt.DB
	httpClient *httpclient.Client
}

// NewBBoltDBService creates a new Service with a default HTTP client.
func NewBBoltDBService(dbPath string) (*BBoltDBService, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	return NewServiceWithClient(dbPath, httpclient.New(httpclient.WithJar(jar)))
}

// NewServiceWithClient creates a new Service with a custom HTTP client.
func NewServiceWithClient(dbPath string, client *httpclient.Client) (*BBoltDBService, error) {
	db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(imagesBucket))
		if err != nil {
			return fmt.Errorf("failed to create images bucket: %w", err)
		}

		return nil
	})
	if err != nil {
		_ = db.Close()
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

func (s *BBoltDBService) Delete(id string) error {
	if id == "" {
		return nil
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		if bucket == nil {
			return nil
		}
		return bucket.Delete([]byte(id))
	})
}

func (s *BBoltDBService) DownloadImageData(ctx context.Context, urlStr string) ([]byte, error) {
	if urlStr == "" {
		return nil, errors.New("URL is required")
	}

	if strings.HasPrefix(strings.ToLower(urlStr), "data:image/") {
		header, dataPart, ok := strings.Cut(urlStr, ",")
		if !ok {
			return nil, errors.New("invalid data URI")
		}

		var data []byte
		if isBase64DataURIHeader(header) {
			dataPart = strings.TrimSpace(dataPart)
			if len(dataPart) > base64.StdEncoding.EncodedLen(maxImageSize) {
				return nil, ErrImageTooLarge
			}

			var err error
			data, err = base64.StdEncoding.DecodeString(dataPart)
			if err != nil {
				data, err = base64.RawStdEncoding.DecodeString(dataPart)
				if err != nil {
					return nil, fmt.Errorf("invalid base64 in data URI: %w", err)
				}
			}
		} else {
			unescaped, err := url.PathUnescape(dataPart)
			if err != nil {
				return nil, fmt.Errorf("invalid data URI encoding: %w", err)
			}
			data = []byte(unescaped)
		}

		if len(data) > maxImageSize {
			return nil, ErrImageTooLarge
		}

		contentType := DetectContentType(data)
		if !IsImageContentType(contentType) {
			return nil, fmt.Errorf("unsupported image type: %s", contentType)
		}

		return data, nil
	}

	resp, err := s.httpClient.Get(ctx, urlStr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d from %s", resp.StatusCode, urlStr)
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
	contentType := DetectContentType(data)
	if !IsImageContentType(contentType) {
		return nil, fmt.Errorf("unsupported image type: %s", contentType)
	}

	return data, nil
}

func isBase64DataURIHeader(header string) bool {
	for value := range strings.SplitSeq(header, ";") {
		if strings.EqualFold(strings.TrimSpace(value), "base64") {
			return true
		}
	}
	return false
}

func (s *BBoltDBService) Get(id string) ([]byte, error) {
	if id == "" {
		return nil, ErrImageNotFound
	}

	var data []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		if bucket == nil {
			return ErrImageNotFound
		}
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
	ids := make([]string, 0)
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		if bucket == nil {
			return nil
		}
		c := bucket.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			ids = append(ids, string(k))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return ids, nil
}

func (s *BBoltDBService) SaveData(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("empty data")
	}
	if len(data) > maxImageSize {
		return "", ErrImageTooLarge
	}

	// Generate ID from data hash.
	hash := sha256.Sum256(data)
	id := hex.EncodeToString(hash[:])

	err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(imagesBucket))
		if bucket == nil {
			return errors.New("images bucket does not exist")
		}

		existing := bucket.Get([]byte(id))
		if existing != nil {
			return nil
		}

		return bucket.Put([]byte(id), data)
	})
	if err != nil {
		return "", fmt.Errorf("failed to save data: %w", err)
	}

	return id, nil
}

func (s *BBoltDBService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/")
	if ext := path.Ext(id); ext != "" {
		for _, imageExt := range ImageTypes {
			if strings.EqualFold(ext, imageExt) {
				id = strings.TrimSuffix(id, ext)
				break
			}
		}
	}
	if len(id) != sha256.Size*2 {
		http.Error(w, ErrImageNotFound.Error(), http.StatusNotFound)
		return
	}
	if _, err := hex.DecodeString(id); err != nil {
		http.Error(w, ErrImageNotFound.Error(), http.StatusNotFound)
		return
	}

	data, err := s.Get(id)
	if err != nil {
		st := http.StatusInternalServerError
		if errors.Is(err, ErrImageNotFound) {
			st = http.StatusNotFound
		}
		http.Error(w, err.Error(), st)
		return
	}

	contentType := DetectContentType(data)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+id+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if contentType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
	}

	http.ServeContent(w, r, id, time.Time{}, bytes.NewReader(data))
}
