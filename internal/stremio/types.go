// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stremio

// Manifest defines the top-level metadata of the Stremio addon.
type Manifest struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	Description   string         `json:"description"`
	Logo          string         `json:"logo,omitempty"`
	Background    string         `json:"background,omitempty"`
	Resources     []string       `json:"resources"`
	Types         []string       `json:"types"`
	Catalogs      []Catalog      `json:"catalogs"`
	IDPrefixes    []string       `json:"idPrefixes,omitempty"`
	BehaviorHints *BehaviorHints `json:"behaviorHints,omitempty"`
}

// Catalog defines a discoverable catalog of items in Stremio.
type Catalog struct {
	Type  string      `json:"type"`
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Extra []ExtraProp `json:"extra,omitempty"`
}

// ExtraProp defines extra filtering options supported by a catalog (e.g. search, skip).
type ExtraProp struct {
	Name       string   `json:"name"`
	IsRequired bool     `json:"isRequired,omitempty"`
	Options    []string `json:"options,omitempty"`
}

// BehaviorHints defines addon behavioral parameters.
type BehaviorHints struct {
	Configurable          bool `json:"configurable,omitempty"`
	ConfigurationRequired bool `json:"configurationRequired,omitempty"`
}

// CatalogResponse is the response returned for a catalog query.
type CatalogResponse struct {
	Metas []MetaPreview `json:"metas"`
}

// MetaPreview represents an item in a catalog listing.
type MetaPreview struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Poster      string   `json:"poster,omitempty"`
	Background  string   `json:"background,omitempty"`
	Logo        string   `json:"logo,omitempty"`
	Description string   `json:"description,omitempty"`
	ReleaseInfo string   `json:"releaseInfo,omitempty"`
	Genres      []string `json:"genres,omitempty"`
}

// MetaResponse is the response returned for a metadata query.
type MetaResponse struct {
	Meta *MetaDetail `json:"meta"`
}

// MetaDetail represents full metadata details for a media item.
type MetaDetail struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	Name          string             `json:"name"`
	Genres        []string           `json:"genres,omitempty"`
	Poster        string             `json:"poster,omitempty"`
	Background    string             `json:"background,omitempty"`
	Logo          string             `json:"logo,omitempty"`
	Description   string             `json:"description,omitempty"`
	ReleaseInfo   string             `json:"releaseInfo,omitempty"`
	Videos        []Video            `json:"videos,omitempty"`
	BehaviorHints *MetaBehaviorHints `json:"behaviorHints,omitempty"`
}

// MetaBehaviorHints provides hints for item playback in Stremio.
type MetaBehaviorHints struct {
	DefaultVideoID string `json:"defaultVideoId,omitempty"`
}

// Video represents an episode or video file in Stremio.
type Video struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Released string `json:"released,omitempty"`
	Season   int    `json:"season,omitempty"`
	Episode  int    `json:"episode,omitempty"`
}

// StreamResponse is the response returned for a stream query.
type StreamResponse struct {
	Streams []Stream `json:"streams"`
}

// Stream represents a playable media stream in Stremio.
type Stream struct {
	Name          string               `json:"name,omitempty"`
	Title         string               `json:"title,omitempty"`
	URL           string               `json:"url"`
	BehaviorHints *StreamBehaviorHints `json:"behaviorHints,omitempty"`
}

// StreamBehaviorHints provides hints for stream player behavior.
type StreamBehaviorHints struct {
	NotWebReady bool   `json:"notWebReady,omitempty"`
	BingeGroup  string `json:"bingeGroup,omitempty"`
}
