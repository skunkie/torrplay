// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package httpclient

import (
	"context"
	"net/http"
	"net/http/cookiejar"
)

const userAgent = "TorrPlay/1.0"

type Client struct {
	client *http.Client
}

func New(opts ...option) *Client {
	client := &Client{client: &http.Client{}}

	for _, opt := range opts {
		opt(client)
	}

	return client
}

func NewWithClient(client *http.Client) *Client {
	return &Client{client: client}
}

type option func(*Client)

func WithJar(jar *cookiejar.Jar) func(*Client) {
	return func(c *Client) {
		c.client.Jar = jar
	}
}

func (c *Client) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", userAgent)

	return c.client.Do(req)
}

func (c *Client) Get(ctx context.Context, url string) (resp *http.Response, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	return c.Do(req)
}
