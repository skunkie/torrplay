// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package images

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_DownloadAndSaveData(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "posters.db")
	s, err := NewBBoltDBService(dbPath)
	require.NoError(t, err)
	defer s.Close()

	// Test with a data URI.
	posterURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	data, err := s.DownloadImageData(ctx, posterURL)
	require.NoError(t, err)
	id, err := s.SaveData(data)
	require.NoError(t, err)
	assert.NotEmpty(t, id)

	// Test with a real image URL.
	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 6, 0, 0, 0, 31, 21, 196, 137, 0, 0, 0, 10, 73, 68, 65, 84, 120, 156, 99, 0, 1, 0, 0, 5, 0, 1, 13, 10, 45, 180, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130})
	}))
	defer imageServer.Close()

	data2, err := s.DownloadImageData(ctx, imageServer.URL)
	require.NoError(t, err)
	id2, err := s.SaveData(data2)
	require.NoError(t, err)
	assert.NotEmpty(t, id2)
	assert.NotEqual(t, id, id2)

	// Verify ListIDs
	ids, err := s.ListIDs()
	require.NoError(t, err)
	assert.Contains(t, ids, id)
	assert.Contains(t, ids, id2)

	// Test getting the images via ServeHTTP.
	req := httptest.NewRequest(http.MethodGet, "/"+id, http.NoBody)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/png", rr.Header().Get("Content-Type"))
	assert.Equal(t, `"`+id+`"`, rr.Header().Get("ETag"))
	assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, rr.Header().Get("Cache-Control"), "immutable")

	// Test getting with extension stripped.
	reqExt := httptest.NewRequest(http.MethodGet, "/"+id+".png", http.NoBody)
	rrExt := httptest.NewRecorder()
	s.ServeHTTP(rrExt, reqExt)
	assert.Equal(t, http.StatusOK, rrExt.Code)

	// Test conditional request (If-None-Match -> 304).
	reqETag := httptest.NewRequest(http.MethodGet, "/"+id, http.NoBody)
	reqETag.Header.Set("If-None-Match", `"`+id+`"`)
	rrETag := httptest.NewRecorder()
	s.ServeHTTP(rrETag, reqETag)
	assert.Equal(t, http.StatusNotModified, rrETag.Code)

	// Test HEAD request.
	reqHead := httptest.NewRequest(http.MethodHead, "/"+id, http.NoBody)
	rrHead := httptest.NewRecorder()
	s.ServeHTTP(rrHead, reqHead)
	assert.Equal(t, http.StatusOK, rrHead.Code)
	assert.Empty(t, rrHead.Body.Bytes())

	// Test unsupported method (POST -> 405).
	reqPost := httptest.NewRequest(http.MethodPost, "/"+id, http.NoBody)
	rrPost := httptest.NewRecorder()
	s.ServeHTTP(rrPost, reqPost)
	assert.Equal(t, http.StatusMethodNotAllowed, rrPost.Code)

	// Test Delete.
	err = s.Delete(id)
	require.NoError(t, err)

	_, err = s.Get(id)
	assert.ErrorIs(t, err, ErrImageNotFound)

	// Delete non-existent ID should succeed as no-op.
	err = s.Delete("non-existent")
	require.NoError(t, err)

	// Delete empty string should succeed as no-op.
	err = s.Delete("")
	require.NoError(t, err)
}

func TestService_Invalid(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "posters.db")
	s, err := NewBBoltDBService(dbPath)
	require.NoError(t, err)
	defer s.Close()

	// Empty URL.
	_, err = s.DownloadImageData(ctx, "")
	assert.Error(t, err)

	// Invalid data URI.
	_, err = s.DownloadImageData(ctx, "data:image/png;base64,invalid-data")
	assert.Error(t, err)

	// Oversized base64 is rejected before decoding it.
	_, err = s.DownloadImageData(ctx, "data:image/png;base64,"+strings.Repeat("A", base64EncodedLimit()+1))
	assert.ErrorIs(t, err, ErrImageTooLarge)

	// Data URI missing comma.
	_, err = s.DownloadImageData(ctx, "data:image/png;base64")
	assert.Error(t, err)

	// Non-existent image URL.
	_, err = s.DownloadImageData(ctx, "http://localhost:12345/image.jpg")
	assert.Error(t, err)

	// Non-200 HTTP response.
	statusServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer statusServer.Close()
	_, err = s.DownloadImageData(ctx, statusServer.URL)
	assert.Error(t, err)

	// Unsupported content type.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	_, err = s.DownloadImageData(ctx, server.URL)
	assert.Error(t, err)

	// Fake image data.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-image-data"))
	}))
	defer server2.Close()

	_, err = s.DownloadImageData(ctx, server2.URL)
	assert.Error(t, err)

	// HTTP download exceeding maxImageSize.
	largeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(make([]byte, maxImageSize+100))
	}))
	defer largeServer.Close()
	_, err = s.DownloadImageData(ctx, largeServer.URL)
	assert.ErrorIs(t, err, ErrImageTooLarge)

	// SaveData with empty data.
	_, err = s.SaveData([]byte{})
	assert.Error(t, err)

	// SaveData with oversized data.
	_, err = s.SaveData(make([]byte, maxImageSize+100))
	assert.ErrorIs(t, err, ErrImageTooLarge)

	// Get with empty ID.
	_, err = s.Get("")
	assert.ErrorIs(t, err, ErrImageNotFound)

	// Get with non-existent ID.
	_, err = s.Get("doesnotexist")
	assert.ErrorIs(t, err, ErrImageNotFound)

	// ServeHTTP with non-existent ID.
	req := httptest.NewRequest(http.MethodGet, "/doesnotexist", http.NoBody)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestService_SVG(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "posters.db")
	s, err := NewBBoltDBService(dbPath)
	require.NoError(t, err)
	defer s.Close()

	svgData := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><rect width="10" height="10"/></svg>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(svgData))
	}))
	defer server.Close()

	data, err := s.DownloadImageData(ctx, server.URL)
	require.NoError(t, err)
	id, err := s.SaveData(data)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/"+id, http.NoBody)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/svg+xml", rr.Header().Get("Content-Type"))
	assert.Equal(t, "default-src 'none'", rr.Header().Get("Content-Security-Policy"))
	assert.Equal(t, "nosniff", rr.Header().Get("X-Content-Type-Options"))

	// A non-base64 data URI preserves literal plus signs.
	data, err = s.DownloadImageData(ctx, `data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg"><text>+</text></svg>`)
	require.NoError(t, err)
	assert.Contains(t, string(data), ">+<")

	// Merely containing an SVG tag does not make an HTML document an image.
	assert.NotEqual(t, "image/svg+xml", DetectContentType([]byte(`<html><svg></svg></html>`)))
	assert.NotEqual(t, "image/svg+xml", DetectContentType([]byte(`<svg><g></svg>`)))
}

func base64EncodedLimit() int {
	return base64.StdEncoding.EncodedLen(maxImageSize)
}

func TestService_BMPAndAVIF(t *testing.T) {
	ctx := t.Context()
	dbPath := filepath.Join(t.TempDir(), "posters.db")
	s, err := NewBBoltDBService(dbPath)
	require.NoError(t, err)
	defer s.Close()

	// BMP header: 'BM' + 12 bytes minimum
	bmpBytes := append([]byte("BM"), make([]byte, 12)...)
	serverBMP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bmpBytes)
	}))
	defer serverBMP.Close()

	dataBMP, err := s.DownloadImageData(ctx, serverBMP.URL)
	require.NoError(t, err)
	assert.Equal(t, "image/bmp", DetectContentType(dataBMP))

	// AVIF header: 4 bytes length + 'ftyp' + 'avif'
	avifBytes := append([]byte{0, 0, 0, 16}, []byte("ftypavif")...)
	serverAVIF := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(avifBytes)
	}))
	defer serverAVIF.Close()

	dataAVIF, err := s.DownloadImageData(ctx, serverAVIF.URL)
	require.NoError(t, err)
	assert.Equal(t, "image/avif", DetectContentType(dataAVIF))
}

func TestService_DuplicateSaveData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "posters.db")
	s, err := NewBBoltDBService(dbPath)
	require.NoError(t, err)
	defer s.Close()

	data := []byte("BM000000000000")
	id1, err := s.SaveData(data)
	require.NoError(t, err)

	id2, err := s.SaveData(data)
	require.NoError(t, err)
	assert.Equal(t, id1, id2)
}

func TestService_WithCookieJar(t *testing.T) {
	ctx := t.Context()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "loggedin"})
			w.WriteHeader(http.StatusOK)
			return
		}

		if r.URL.Path == "/image.jpg" {
			cookie, err := r.Cookie("session")
			if err != nil || cookie.Value != "loggedin" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte{255, 216, 255, 224, 0, 16, 74, 70, 73, 70, 0, 1, 1, 1, 0, 72, 0, 72, 0, 0, 255, 219, 0, 67, 0, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 255, 192, 0, 17, 8, 0, 1, 0, 1, 3, 1, 34, 0, 2, 17, 1, 3, 17, 1, 255, 196, 0, 31, 0, 0, 1, 5, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 255, 196, 0, 181, 16, 0, 2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0, 0, 1, 125, 1, 2, 3, 0, 4, 17, 5, 18, 33, 49, 65, 6, 19, 81, 97, 7, 34, 113, 20, 50, 129, 145, 161, 8, 35, 66, 177, 193, 21, 82, 209, 240, 36, 51, 98, 114, 130, 9, 10, 22, 23, 24, 25, 26, 37, 38, 39, 40, 41, 42, 52, 53, 54, 55, 56, 57, 58, 67, 68, 69, 70, 71, 72, 73, 74, 83, 84, 85, 86, 87, 88, 89, 90, 99, 100, 101, 102, 103, 104, 105, 106, 115, 116, 117, 118, 119, 120, 121, 122, 131, 132, 133, 134, 135, 136, 137, 138, 146, 147, 148, 149, 150, 151, 152, 153, 154, 162, 163, 164, 165, 166, 167, 168, 169, 170, 178, 179, 180, 181, 182, 183, 184, 185, 186, 194, 195, 196, 197, 198, 199, 200, 201, 202, 210, 211, 212, 213, 214, 215, 216, 217, 218, 225, 226, 227, 228, 229, 230, 231, 232, 233, 234, 241, 242, 243, 244, 245, 246, 247, 248, 249, 250, 255, 218, 0, 12, 3, 1, 0, 2, 17, 3, 17, 0, 63, 0, 247, 177, 154, 0, 15, 141, 191, 155, 248, 34, 254, 52, 63, 255, 217})
		}
	}))
	defer server.Close()

	dbPath := filepath.Join(t.TempDir(), "posters.db")
	s, err := NewBBoltDBService(dbPath)
	require.NoError(t, err)
	defer s.Close()

	resp, err := s.httpClient.Get(ctx, server.URL+"/login")
	require.NoError(t, err)
	defer resp.Body.Close()

	data, err := s.DownloadImageData(ctx, server.URL+"/image.jpg")
	require.NoError(t, err)
	id, err := s.SaveData(data)
	require.NoError(t, err)
	assert.NotEmpty(t, id)
}

func TestUnimplemented(t *testing.T) {
	u := Unimplemented{}
	assert.ErrorIs(t, u.Close(), ErrUnimplemented)
	assert.ErrorIs(t, u.Delete(""), ErrUnimplemented)
	_, err := u.DownloadImageData(context.Background(), "")
	assert.ErrorIs(t, err, ErrUnimplemented)
	_, err = u.Get("")
	assert.ErrorIs(t, err, ErrUnimplemented)
	_, err = u.ListIDs()
	assert.ErrorIs(t, err, ErrUnimplemented)
	_, err = u.SaveData(nil)
	assert.ErrorIs(t, err, ErrUnimplemented)

	rr := httptest.NewRecorder()
	u.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	assert.Equal(t, http.StatusNotImplemented, rr.Code)
}

func TestNewBBoltDBService_Error(t *testing.T) {
	_, err := NewBBoltDBService(filepath.Join(string(filepath.Separator), "dev", "null", "impossible", "db"))
	assert.Error(t, err)
}
