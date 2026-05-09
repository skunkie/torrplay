// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package metadata

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	torrentparser "github.com/ProfChaos/torrent-name-parser"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/torrplay/torrplay/internal/api"
)

var thetvdbBaseURL = "https://api4.thetvdb.com/v4"

// TVDBClient is a client for TheTVDB API.
type TVDBClient struct {
	httpClient *http.Client
	apiKey     string
	token      string
}

// NewTVDBClient creates a new TheTVDB client.
func NewTVDBClient(apiKey string) (*TVDBClient, error) {
	c := &TVDBClient{
		httpClient: &http.Client{},
		apiKey:     apiKey,
	}

	if err := c.login(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *TVDBClient) login() error {
	loginData := map[string]string{"apikey": c.apiKey}
	loginBody, err := json.Marshal(loginData)
	if err != nil {
		return err
	}

	resp, err := c.httpPost(fmt.Sprintf("%s/login", thetvdbBaseURL), loginBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var loginResponse struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&loginResponse); err != nil {
		return err
	}

	c.token = loginResponse.Data.Token

	return nil
}

// Search searches for a series or movie by name.
func (c *TVDBClient) Search(name string, year int, searchType string, language string) ([]byte, error) {
	params := url.Values{}
	params.Add("query", name)
	if year > 0 {
		params.Add("year", fmt.Sprintf("%d", year))
	}
	params.Add("type", searchType)
	if language != "" {
		params.Add("language", language)
	}

	return c.httpGet(fmt.Sprintf("%s/search?%s", thetvdbBaseURL, params.Encode()))
}

func (c *TVDBClient) httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func (c *TVDBClient) httpPost(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	c.setAuthHeader(req)

	return c.httpClient.Do(req)
}

func (c *TVDBClient) setAuthHeader(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.token))
	}
}

func (c *TVDBClient) UpdateMetadata(backup api.Backup, opts Options) (api.Backup, error) {
	searchTypeSeries := "series"
	searchTypeMovie := "movie"

	for i := range backup.Torrents {
		t := &backup.Torrents[i]

		parsed, err := torrentparser.ParseName(t.Name)
		if err != nil {
			fmt.Printf("failed to parse torrent name: %s\n", t.Name)
			continue
		}

		var searchType string
		if parsed.Season > 0 || parsed.Episode > 0 {
			searchType = searchTypeSeries
		} else {
			searchType = searchTypeMovie
		}

		if opts.Category && (t.Category == nil || *t.Category == "") {
			var category string
			switch searchType {
			case searchTypeSeries:
				category = "Series"
			case searchTypeMovie:
				category = "Movies"
			}
			t.Category = &category
		}

		if opts.Poster || opts.Title {
			searchResult, err := c.Search(parsed.Title, parsed.Year, searchType, opts.Language)
			if err != nil {
				fmt.Printf("failed to search for media: %v\n", err)
				continue
			}

			var searchResponse struct {
				Data []struct {
					ID       string `json:"tvdb_id"`
					ImageURL string `json:"image_url"`
					Name     string `json:"name"`
				} `json:"data"`
			}
			if err := json.Unmarshal(searchResult, &searchResponse); err != nil {
				fmt.Printf("failed to parse search result: %v\n", err)
				continue
			}

			if len(searchResponse.Data) == 0 && parsed.Year != 0 {
				fmt.Printf("no results found for '%s' with year %d, retrying without year...\n", parsed.Title, parsed.Year)
				searchResult, err = c.Search(parsed.Title, 0, searchType, opts.Language)
				if err != nil {
					fmt.Printf("failed to search for media: %v\n", err)
					continue
				}
				if err := json.Unmarshal(searchResult, &searchResponse); err != nil {
					fmt.Printf("failed to parse search result: %v\n", err)
					continue
				}
			}

			if len(searchResponse.Data) == 0 {
				fmt.Printf("no results found for '%s'\n", t.Name)
				continue
			}

			if opts.Title {
				t.Title = &searchResponse.Data[0].Name
			}

			if opts.Poster {
				if t.Poster != nil && *t.Poster != "" {
					continue
				}

				posterURL := searchResponse.Data[0].ImageURL

				if posterURL != "" {
					fmt.Printf("fetching poster from: %s\n", posterURL)
					resp, err := http.Get(posterURL)
					if err != nil {
						fmt.Printf("failed to fetch poster image: %v\n", err)
						continue
					}
					defer resp.Body.Close()

					posterData, err := io.ReadAll(resp.Body)
					if err != nil {
						fmt.Printf("failed to read poster image data: %v\n", err)
						continue
					}

					hash := sha256.Sum256(posterData)
					posterID := hex.EncodeToString(hash[:])
					t.Poster = &posterID

					file := openapi_types.File{}
					file.InitFromBytes(posterData, posterID)
					backup.Posters[posterID] = file
					fmt.Printf("downloaded poster: %s\n", posterURL)
				}
			}
		}
	}

	return backup, nil
}
