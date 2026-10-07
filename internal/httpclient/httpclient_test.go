// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package httpclient

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)

	client := New(WithJar(jar))
	assert.Equal(t, jar, client.client.Jar)
}

func TestDo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, userAgent, r.Header.Get("User-Agent"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestGet(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New()
	resp, err := client.Get(t.Context(), server.URL)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New()

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	resp, err := client.Get(ctx, server.URL)
	if resp != nil {
		defer resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestGetLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/redirect", http.StatusFound)
		case "/scheme":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		case "/status":
			w.WriteHeader(http.StatusNotFound)
		case "/timeout":
			<-r.Context().Done()
		default:
			_, _ = w.Write([]byte("12345"))
		}
	}))
	defer server.Close()
	client := NewWithClient(&http.Client{Timeout: 20 * time.Millisecond})
	for _, tc := range []struct {
		name, url string
		limit     int64
		fails     bool
	}{
		{"exact limit", server.URL, 5, false},
		{"under limit", server.URL, 6, false},
		{"oversize", server.URL, 4, true},
		{"status", server.URL + "/status", 5, true},
		{"redirect cap", server.URL + "/redirect", 5, true},
		{"redirect scheme", server.URL + "/scheme", 5, true},
		{"timeout", server.URL + "/timeout", 5, true},
		{"scheme", "file:///etc/passwd", 5, true},
		{"host", "http:///path", 5, true},
		{"malformed", "http://%", 5, true},
		{"negative limit", server.URL, -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := client.GetLimited(t.Context(), tc.url, tc.limit)
			if tc.fails {
				require.Error(t, err)
				assert.Nil(t, body)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "12345", string(body))
			}
		})
	}
	assert.Equal(t, 30*time.Second, New().client.Timeout)
	assert.Equal(t, 30*time.Second, NewWithClient(&http.Client{Timeout: time.Minute}).client.Timeout)
}
