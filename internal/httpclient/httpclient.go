// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package httpclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"
)

const userAgent = "TorrPlay/1.0"

type Client struct {
	client *http.Client
}

func New(opts ...option) *Client {
	client := NewWithClient(&http.Client{})

	for _, opt := range opts {
		opt(client)
	}

	return client
}

func NewWithClient(client *http.Client) *Client {
	bounded := *client
	if bounded.Timeout == 0 || bounded.Timeout > 30*time.Second {
		bounded.Timeout = 30 * time.Second
	}
	previousRedirect := bounded.CheckRedirect
	bounded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := ValidateURL(req.URL.String()); err != nil {
			return err
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	return &Client{client: &bounded}
}

type option func(*Client)

func WithJar(jar *cookiejar.Jar) func(*Client) {
	return func(c *Client) {
		c.client.Jar = jar
	}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if err := ValidateURL(req.URL.String()); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	return c.client.Do(req)
}

func (c *Client) Get(ctx context.Context, rawURL string) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return nil, err
	}

	return c.Do(req)
}

// ValidateURL permits LAN indexers intentionally: private addresses are valid sources.
// Only HTTP(S) is allowed, including after redirects; local file URLs are never read.
func ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("URL must use http or https and include a host")
	}
	return nil
}

// GetLimited fetches a successful HTTP response with a bounded body size.
func (c *Client) GetLimited(ctx context.Context, rawURL string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 || maxBytes == int64(^uint64(0)>>1) {
		return nil, errors.New("invalid body size limit")
	}
	resp, err := c.Get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}
	return body, nil
}
