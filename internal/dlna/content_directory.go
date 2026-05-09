// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"mime"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethulhu/helix/media"
	"github.com/ethulhu/helix/upnpav"
	"github.com/ethulhu/helix/upnpav/contentdirectory"
	"github.com/ethulhu/helix/upnpav/contentdirectory/search"
	"github.com/ethulhu/helix/xmltypes"
	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/database"
	"github.com/torrplay/torrplay/internal/images"
	"github.com/torrplay/torrplay/internal/utils"
)

const (
	rootID                    = "0"
	allTorrentsContainerID    = "1"
	recentlyAddedContainerID  = "2"
	recentlyViewedContainerID = "3"

	allTorrentsContainer    = "All"
	recentlyAddedContainer  = "Recently Added"
	recentlyViewedContainer = "Recently Viewed"

	recentlyItemsCount = 10
)

type (
	ContentDirectory struct {
		baseURL        *url.URL
		db             database.DatabaseInterface
		images         images.ServiceInterface
		mu             sync.RWMutex
		postersPath    string
		systemUpdateID uint
	}

	Features struct {
		XMLName        xml.Name `xml:"Features"`
		Xmlns          string   `xml:"xmlns,attr"`
		XmlnsXSI       string   `xml:"xmlns:xsi,attr"`
		SchemaLocation string   `xml:"xsi:schemaLocation,attr"`
		Feature        Feature  `xml:"Feature"`
	}

	Feature struct {
		Name       string      `xml:"name,attr"`
		Version    int         `xml:"version,attr"`
		Containers []Container `xml:"container"`
	}

	Container struct {
		ID   string `xml:"id,attr"`
		Type string `xml:"type,attr"`
	}
)

func NewContentDirectory(db database.DatabaseInterface, images images.ServiceInterface, baseURL *url.URL, postersPath string) *ContentDirectory {
	return &ContentDirectory{
		baseURL:        baseURL,
		db:             db,
		images:         images,
		postersPath:    postersPath,
		systemUpdateID: uint(time.Now().Unix()),
	}
}

func (cd *ContentDirectory) BrowseMetadata(ctx context.Context, id upnpav.ObjectID, filter xmltypes.CommaSeparatedStrings) (*upnpav.DIDLLite, error) {
	if id == rootID {
		return &upnpav.DIDLLite{
			Containers: []upnpav.Container{
				{
					ID:         rootID,
					Parent:     "-1",
					Title:      "TorrPlay",
					Class:      upnpav.StorageFolder,
					Restricted: true,
					Searchable: true,
					ChildCount: 3,
				},
			},
		}, nil
	}

	if id == allTorrentsContainerID || id == recentlyAddedContainerID || id == recentlyViewedContainerID {
		return cd.browseTorrents(ctx, id)
	}

	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, err
	}

	for _, torrent := range torrents {
		if torrent.Hash.HexString() == string(id) {
			didl := &upnpav.DIDLLite{}
			date := &upnpav.Date{Time: utils.Val(torrent.CreatedAt)}
			if torrent.UpdatedAt != nil {
				date = &upnpav.Date{Time: *torrent.UpdatedAt}
			}

			container := upnpav.Container{
				ID:         id,
				Parent:     allTorrentsContainerID,
				Title:      torrent.Name,
				Class:      upnpav.StorageFolder,
				Restricted: true,
				Searchable: true,
				Date:       date,
			}
			childCount := 0
			for _, file := range torrent.Files {
				if isMediaFile(file) {
					childCount++
				}
			}
			container.ChildCount = childCount
			didl.Containers = append(didl.Containers, container)
			return didl, nil
		}
	}

	return nil, contentdirectory.ErrNoSuchObject
}

func (cd *ContentDirectory) BrowseChildren(ctx context.Context, parentID upnpav.ObjectID, filter xmltypes.CommaSeparatedStrings) (*upnpav.DIDLLite, error) {
	if parentID == rootID {
		return cd.browseRoot(ctx)
	}

	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, err
	}

	var torrentsByParentID []*api.Torrent
	switch parentID {
	case allTorrentsContainerID:
		torrentsByParentID = torrents
	case recentlyAddedContainerID:
		torrentsByParentID = getRecentlyAddedTorrents(torrents)
	case recentlyViewedContainerID:
		torrentsByParentID = getRecentlyViewedTorrents(torrents)
	default:
		return cd.browseTorrent(ctx, parentID)
	}

	return cd.buildTorrentsDIDL(ctx, parentID, torrentsByParentID)
}

func (cd *ContentDirectory) IncrementSystemUpdateID() {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.systemUpdateID++
}

func (cd *ContentDirectory) Search(_ context.Context, id upnpav.ObjectID, criteria search.Criteria) (*upnpav.DIDLLite, error) {
	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, fmt.Errorf("could not get torrents: %w", err)
	}

	didl := &upnpav.DIDLLite{}
	for _, torrent := range torrents {
		if !hasMediaFiles(torrent.Files) {
			continue
		}

		critStr := criteria.String()
		if after, ok := strings.CutPrefix(critStr, `(dc:title contains "`); ok {
			searchTerm := after
			searchTerm = strings.TrimSuffix(searchTerm, `")`)
			if !strings.Contains(torrent.Name, searchTerm) {
				continue
			}
		}

		parentID := allTorrentsContainerID

		if id != rootID {
			parentID = string(id)
		}

		date := &upnpav.Date{Time: utils.Val(torrent.CreatedAt)}
		if torrent.UpdatedAt != nil {
			date = &upnpav.Date{Time: *torrent.UpdatedAt}
		}
		container := upnpav.Container{
			ID:         upnpav.ObjectID(torrent.Hash.HexString()),
			Parent:     upnpav.ObjectID(parentID),
			Title:      torrent.Name,
			Class:      upnpav.StorageFolder,
			Restricted: true,
			Searchable: true,
			Date:       date,
		}
		childCount := 0
		for _, file := range torrent.Files {
			if isMediaFile(file) {
				childCount++
			}
		}
		container.ChildCount = childCount
		didl.Containers = append(didl.Containers, container)
	}

	sort.SliceStable(didl.Containers, func(i, j int) bool {
		return didl.Containers[i].Title < didl.Containers[j].Title
	})

	return didl, nil
}

func (cd *ContentDirectory) SearchCapabilities(_ context.Context) ([]string, error) {
	return []string{"dc:title"}, nil
}
func (cd *ContentDirectory) SortCapabilities(_ context.Context) ([]string, error) {
	return []string{"dc:title", "dc:date"}, nil
}
func (cd *ContentDirectory) SystemUpdateID(_ context.Context) (uint, error) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return cd.systemUpdateID, nil
}

func (cd *ContentDirectory) XGetFeatureList(_ context.Context) ([]string, error) {
	features := Features{
		Xmlns:          "urn:schemas-upnp-org:av:avs",
		XmlnsXSI:       "http://www.w3.org/2001/XMLSchema-instance",
		SchemaLocation: "urn:schemas-upnp-org:av:avs http://www.upnp.org/schemas/av/avs.xsd",
		Feature: Feature{
			Name:    "samsung.com_BASICVIEW",
			Version: 1,
			Containers: []Container{
				{ID: "0", Type: "object.item.audioItem"},
				{ID: "0", Type: "object.item.videoItem"},
				{ID: "0", Type: "object.item.imageItem"},
			},
		},
	}

	bytes, err := xml.Marshal(features)
	if err != nil {
		return nil, err
	}

	return []string{string(bytes)}, nil
}

func (cd *ContentDirectory) browseRoot(_ context.Context) (*upnpav.DIDLLite, error) {
	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, err
	}

	var all []*api.Torrent
	for _, torrent := range torrents {
		if hasMediaFiles(torrent.Files) {
			all = append(all, torrent)
		}
	}

	recentlyAdded := getRecentlyAddedTorrents(all)
	recentlyViewed := getRecentlyViewedTorrents(all)

	return &upnpav.DIDLLite{
		Containers: []upnpav.Container{
			{
				ID:         allTorrentsContainerID,
				Parent:     rootID,
				Title:      allTorrentsContainer,
				Class:      upnpav.StorageFolder,
				Restricted: true,
				Searchable: true,
				ChildCount: len(all),
			},
			{
				ID:         recentlyAddedContainerID,
				Parent:     rootID,
				Title:      recentlyAddedContainer,
				Class:      upnpav.StorageFolder,
				Restricted: true,
				Searchable: true,
				ChildCount: len(recentlyAdded),
			},
			{
				ID:         recentlyViewedContainerID,
				Parent:     rootID,
				Title:      recentlyViewedContainer,
				Class:      upnpav.StorageFolder,
				Restricted: true,
				Searchable: true,
				ChildCount: len(recentlyViewed),
			},
		},
	}, nil
}

func (cd *ContentDirectory) browseTorrents(_ context.Context, id upnpav.ObjectID) (*upnpav.DIDLLite, error) {
	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, err
	}

	var childCount int
	var title string

	switch id {
	case allTorrentsContainerID:
		title = allTorrentsContainer
		var all []*api.Torrent
		for _, torrent := range torrents {
			if hasMediaFiles(torrent.Files) {
				all = append(all, torrent)
			}
		}
		childCount = len(all)
	case recentlyAddedContainerID:
		title = recentlyAddedContainer
		childCount = len(getRecentlyAddedTorrents(torrents))
	case recentlyViewedContainerID:
		title = recentlyViewedContainer
		childCount = len(getRecentlyViewedTorrents(torrents))
	}

	return &upnpav.DIDLLite{
		Containers: []upnpav.Container{
			{
				ID:         id,
				Parent:     rootID,
				Title:      title,
				Class:      upnpav.StorageFolder,
				Restricted: true,
				ChildCount: childCount,
			},
		},
	}, nil
}

func (cd *ContentDirectory) buildTorrentsDIDL(_ context.Context, parentID upnpav.ObjectID, torrents []*api.Torrent) (*upnpav.DIDLLite, error) {
	didl := &upnpav.DIDLLite{}
	for _, torrent := range torrents {
		if !hasMediaFiles(torrent.Files) {
			continue
		}

		date := &upnpav.Date{Time: utils.Val(torrent.CreatedAt)}
		if torrent.UpdatedAt != nil {
			date = &upnpav.Date{Time: *torrent.UpdatedAt}
		}
		container := upnpav.Container{
			ID:         upnpav.ObjectID(torrent.Hash.HexString()),
			Parent:     parentID,
			Title:      torrent.Name,
			Class:      upnpav.StorageFolder,
			Restricted: true,
			Searchable: true,
			Date:       date,
		}
		childCount := 0
		for _, file := range torrent.Files {
			if isMediaFile(file) {
				childCount++
			}
		}
		container.ChildCount = childCount
		didl.Containers = append(didl.Containers, container)
	}

	if parentID == allTorrentsContainerID {
		sort.SliceStable(didl.Containers, func(i, j int) bool {
			return didl.Containers[i].Title < didl.Containers[j].Title
		})
	}

	return didl, nil
}

func (cd *ContentDirectory) browseTorrent(_ context.Context, torrentHash upnpav.ObjectID) (*upnpav.DIDLLite, error) {
	ih, err := utils.HashFromHexString(string(torrentHash))
	if err != nil {
		return nil, err
	}
	torrent, err := cd.db.GetTorrent(ih)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(torrent.Files, func(a, b api.TorrentFile) int {
		return strings.Compare(a.Path, b.Path)
	})

	didl := &upnpav.DIDLLite{}
	for i, file := range torrent.Files {
		if !isMediaFile(file) {
			continue
		}

		class, err := upnpav.ClassForMIMEType(mime.TypeByExtension(path.Ext(file.Path)))
		if err != nil {
			continue
		}

		iconURI, err := cd.iconURI(file.Path)
		if err != nil {
			return nil, err
		}

		resources := []upnpav.Resource{
			{
				URI: cd.fileURI(torrent.Hash.HexString(), file.Path),
				ProtocolInfo: &upnpav.ProtocolInfo{
					Protocol:       upnpav.ProtocolHTTP,
					ContentFormat:  mime.TypeByExtension(path.Ext(file.Path)),
					AdditionalInfo: upnpav.ContentFeatures,
				},
				SizeBytes: uint(file.Length),
			},
		}

		if iconURI.String() != "" {
			resources = append(resources, upnpav.Resource{
				URI: iconURI.String(),
				ProtocolInfo: &upnpav.ProtocolInfo{
					Protocol:       upnpav.ProtocolHTTP,
					ContentFormat:  "image/png",
					AdditionalInfo: "DLNA.ORG_PN=PNG_LRG",
				},
			})
		}

		item := upnpav.Item{
			ID:        upnpav.ObjectID(fmt.Sprintf("%s/%d", torrent.Hash.HexString(), i)),
			Parent:    upnpav.ObjectID(torrent.Hash.HexString()),
			Title:     file.Name,
			Class:     class,
			Icon:      iconURI,
			Resources: resources,
		}
		didl.Items = append(didl.Items, item)
	}

	return didl, nil
}

func (cd *ContentDirectory) fileURI(hash string, filepath string) string {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	if cd.baseURL == nil {
		slog.Error("DLNA ContentDirectory has no base URL set, can't generate file URI")
		return ""
	}

	fileURL := *cd.baseURL
	fileURL.Path = path.Join("/api/v1/stream", hash)
	q := url.Values{}
	q.Set("path", filepath)
	fileURL.RawQuery = q.Encode()

	return fileURL.String()
}

func (cd *ContentDirectory) iconURI(filepath string) (*url.URL, error) {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	if cd.baseURL == nil {
		return nil, fmt.Errorf("DLNA ContentDirectory has no base URL set, can't generate icon URI")
	}

	mediaType := strings.SplitN(mime.TypeByExtension(path.Ext(filepath)), "/", 2)[0]
	iconFilename := fmt.Sprintf("%sfile-128x128.png", mediaType)

	iconURL := *cd.baseURL
	iconURL.Path = path.Join(iconURL.Path, "/icons/media", iconFilename)

	return &iconURL, nil
}

func getRecentlyAddedTorrents(allTorrents []*api.Torrent) []*api.Torrent {
	torrents := make([]*api.Torrent, len(allTorrents))
	copy(torrents, allTorrents)

	sort.Slice(torrents, func(i, j int) bool {
		return utils.Val(torrents[i].CreatedAt).After(utils.Val(torrents[j].CreatedAt))
	})

	if len(torrents) > recentlyItemsCount {
		return torrents[:recentlyItemsCount]
	}

	return torrents
}

func getRecentlyViewedTorrents(allTorrents []*api.Torrent) []*api.Torrent {
	type viewedTorrent struct {
		torrent  *api.Torrent
		viewedAt time.Time
	}

	var viewedTorrents []viewedTorrent

	for _, t := range allTorrents {
		var mostRecentViewTime time.Time
		for _, f := range t.Files {
			if f.ViewedAt != nil && f.ViewedAt.After(mostRecentViewTime) {
				mostRecentViewTime = *f.ViewedAt
			}
		}
		if !mostRecentViewTime.IsZero() {
			viewedTorrents = append(viewedTorrents, viewedTorrent{torrent: t, viewedAt: mostRecentViewTime})
		}
	}

	sort.Slice(viewedTorrents, func(i, j int) bool {
		return viewedTorrents[i].viewedAt.After(viewedTorrents[j].viewedAt)
	})

	if len(viewedTorrents) > recentlyItemsCount {
		viewedTorrents = viewedTorrents[:recentlyItemsCount]
	}

	result := make([]*api.Torrent, len(viewedTorrents))
	for i, wt := range viewedTorrents {
		result[i] = wt.torrent
	}

	return result
}

func hasMediaFiles(files []api.TorrentFile) bool {
	return slices.ContainsFunc(files, isMediaFile)
}

func isMediaFile(file api.TorrentFile) bool {
	return media.IsAudioOrVideo(file.Path) || media.IsImage(file.Path)
}
