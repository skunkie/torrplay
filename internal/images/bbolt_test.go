// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package images

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestService_DownloadAndSaveData(t *testing.T) {
	ctx := t.Context()
	path := tempfile()
	s, err := NewBBoltDBService(path)
	require.NoError(t, err)
	defer os.Remove(path)
	defer s.Close()

	// Test with a data URI.
	posterURL := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII="
	data, err := s.DownloadImageData(ctx, posterURL)
	require.NoError(t, err)
	id, err := s.SaveData(data)
	require.NoError(t, err)
	assert.NotNil(t, id)

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
	assert.NotNil(t, id2)
	assert.NotEqual(t, *id, *id2)

	// Test getting the images.
	req := httptest.NewRequest("GET", "/"+*id, nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/png", rr.Header().Get("Content-Type"))

	req = httptest.NewRequest("GET", "/"+*id2, nil)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/png", rr.Header().Get("Content-Type"))
}

func TestService_Invalid(t *testing.T) {
	ctx := t.Context()
	path := tempfile()
	s, err := NewBBoltDBService(path)
	require.NoError(t, err)
	defer os.Remove(path)
	defer s.Close()

	// Test with an empty URL.
	_, err = s.DownloadImageData(ctx, "")
	assert.Error(t, err)

	// Test with an invalid data URI.
	_, err = s.DownloadImageData(ctx, "data:image/png;base64,invalid-data")
	assert.Error(t, err)

	// Test with a non-existent image URL.
	_, err = s.DownloadImageData(ctx, "http://localhost:12345/image.jpg")
	assert.Error(t, err)

	// Test with an unsupported content type.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html></html>"))
	}))
	defer server.Close()

	_, err = s.DownloadImageData(ctx, server.URL)
	assert.Error(t, err)

	// Test with fake image data.
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("fake-image-data"))
	}))
	defer server2.Close()

	_, err = s.DownloadImageData(ctx, server2.URL)
	assert.Error(t, err)
}

func TestService_WithCookieJar(t *testing.T) {
	ctx := t.Context()
	// Create a test server that requires a cookie.
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

	path := tempfile()
	s, err := NewBBoltDBService(path)
	require.NoError(t, err)
	defer os.Remove(path)
	defer s.Close()

	// Login to get the cookie.
	_, err = s.httpClient.Get(ctx, server.URL+"/login")
	require.NoError(t, err)

	// Try to get the image, which should now work.
	data, err := s.DownloadImageData(ctx, server.URL+"/image.jpg")
	require.NoError(t, err)
	id, err := s.SaveData(data)
	require.NoError(t, err)
	assert.NotNil(t, id)
}

func tempfile() string {
	f, err := os.CreateTemp("", "images-test-")
	if err != nil {
		panic(err)
	}
	if err := f.Close(); err != nil {
		panic(err)
	}
	return f.Name()
}
