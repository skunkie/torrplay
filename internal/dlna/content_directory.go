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
	"strconv"
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

	if strings.Contains(string(id), "/") {
		return cd.browseItemMetadata(ctx, id)
	}

	return cd.browseContainerMetadata(ctx, id)
}

func (cd *ContentDirectory) BrowseChildren(ctx context.Context, parentID upnpav.ObjectID, startingIndex, requestedCount uint, filter xmltypes.CommaSeparatedStrings) (*upnpav.DIDLLite, uint, error) {
	if parentID == rootID {
		return cd.browseRoot(ctx, startingIndex, requestedCount)
	}

	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, 0, err
	}

	var all []*database.Torrent
	for _, torrent := range torrents {
		if hasMediaFiles(torrent.Files) {
			all = append(all, torrent)
		}
	}

	var torrentsByParentID []*database.Torrent
	switch parentID {
	case allTorrentsContainerID:
		torrentsByParentID = all
	case recentlyAddedContainerID:
		torrentsByParentID = getRecentlyAddedTorrents(all)
	case recentlyViewedContainerID:
		torrentsByParentID = getRecentlyViewedTorrents(all)
	default:
		return cd.browseTorrent(ctx, parentID, startingIndex, requestedCount)
	}

	return cd.buildTorrentsDIDL(ctx, parentID, torrentsByParentID, startingIndex, requestedCount)
}

func (cd *ContentDirectory) IncrementSystemUpdateID() {
	cd.mu.Lock()
	defer cd.mu.Unlock()
	cd.systemUpdateID++
}

func (cd *ContentDirectory) Search(_ context.Context, id upnpav.ObjectID, criteria search.Criteria, startingIndex, requestedCount uint, sortCriteria xmltypes.CommaSeparatedStrings) (*upnpav.DIDLLite, uint, error) {
	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, 0, fmt.Errorf("could not get torrents: %w", err)
	}

	var all []*database.Torrent
	for _, torrent := range torrents {
		if hasMediaFiles(torrent.Files) {
			all = append(all, torrent)
		}
	}

	var torrentsToSearch []*database.Torrent
	parentID := allTorrentsContainerID
	searchContainers := true

	switch id {
	case rootID, allTorrentsContainerID:
		torrentsToSearch = all
	case recentlyAddedContainerID:
		torrentsToSearch = getRecentlyAddedTorrents(all)
		parentID = recentlyAddedContainerID
	case recentlyViewedContainerID:
		torrentsToSearch = getRecentlyViewedTorrents(all)
		parentID = recentlyViewedContainerID
	default:
		searchContainers = false
		for _, t := range all {
			if t.Hash.HexString() == string(id) {
				torrentsToSearch = []*database.Torrent{t}
				parentID = string(id)
				break
			}
		}
		if torrentsToSearch == nil {
			return nil, 0, contentdirectory.ErrNoSuchObject
		}
	}

	var matchingContainers []upnpav.Container
	var matchingItems []upnpav.Item

	for _, torrent := range torrentsToSearch {
		mediaFiles := getMediaFiles(torrent.Files)

		if searchContainers {
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
				ChildCount: len(mediaFiles),
			}

			if search.Matches(container, criteria) {
				matchingContainers = append(matchingContainers, container)
			}
		}

		for realIndex, file := range mediaFiles {
			class, err := upnpav.ClassForMIMEType(mime.TypeByExtension(path.Ext(file.Path)))
			if err != nil {
				continue
			}

			var albumArtURIs []string
			var itemIcon *upnpav.URL

			if torrent.Poster != nil && *torrent.Poster != "" {
				if pURI := cd.posterURI(*torrent.Poster); pURI != nil {
					albumArtURIs = []string{pURI.String()}
					itemIcon = pURI
				}
			}

			if itemIcon == nil {
				if iURI := cd.iconURI(file.Path); iURI != nil {
					albumArtURIs = []string{iURI.String()}
					itemIcon = iURI
				}
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

			item := upnpav.Item{
				ID:           upnpav.ObjectID(fmt.Sprintf("%s/%d", torrent.Hash.HexString(), realIndex)),
				Parent:       upnpav.ObjectID(torrent.Hash.HexString()),
				Title:        file.Name,
				Class:        class,
				Restricted:   true,
				Searchable:   true,
				Icon:         itemIcon,
				AlbumArtURIs: albumArtURIs,
				Resources:    resources,
			}

			if search.Matches(item, criteria) {
				matchingItems = append(matchingItems, item)
			}
		}
	}

	sort.SliceStable(matchingContainers, func(i, j int) bool {
		return matchingContainers[i].Title < matchingContainers[j].Title
	})
	sort.SliceStable(matchingItems, func(i, j int) bool {
		return matchingItems[i].Title < matchingItems[j].Title
	})

	totalMatches := uint(len(matchingContainers) + len(matchingItems))

	didl := &upnpav.DIDLLite{
		Containers: matchingContainers,
		Items:      matchingItems,
	}

	return didl.Paginate(startingIndex, requestedCount), totalMatches, nil
}

func (cd *ContentDirectory) SearchCapabilities(_ context.Context) ([]string, error) {
	return []string{"dc:title", "upnp:class"}, nil
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

func (cd *ContentDirectory) browseRoot(_ context.Context, startingIndex, requestedCount uint) (*upnpav.DIDLLite, uint, error) {
	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, 0, err
	}

	var all []*database.Torrent
	for _, torrent := range torrents {
		if hasMediaFiles(torrent.Files) {
			all = append(all, torrent)
		}
	}

	recentlyAdded := getRecentlyAddedTorrents(all)
	recentlyViewed := getRecentlyViewedTorrents(all)

	containers := []upnpav.Container{
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
	}

	totalMatches := uint(len(containers))

	if startingIndex >= uint(len(containers)) {
		return &upnpav.DIDLLite{}, totalMatches, nil
	}

	end := int(startingIndex) + int(requestedCount)
	if end > len(containers) || requestedCount == 0 {
		end = len(containers)
	}

	return &upnpav.DIDLLite{
		Containers: containers[startingIndex:end],
	}, totalMatches, nil
}

func (cd *ContentDirectory) browseContainerMetadata(_ context.Context, id upnpav.ObjectID) (*upnpav.DIDLLite, error) {
	torrents, err := cd.db.GetTorrents()
	if err != nil {
		return nil, err
	}

	var all []*database.Torrent
	for _, torrent := range torrents {
		if hasMediaFiles(torrent.Files) {
			all = append(all, torrent)
		}
	}

	var title string
	var childCount int
	parentID := rootID

	switch id {
	case allTorrentsContainerID:
		title = allTorrentsContainer
		childCount = len(all)
	case recentlyAddedContainerID:
		title = recentlyAddedContainer
		childCount = len(getRecentlyAddedTorrents(all))
	case recentlyViewedContainerID:
		title = recentlyViewedContainer
		childCount = len(getRecentlyViewedTorrents(all))
	default:
		for _, torrent := range all {
			if torrent.Hash.HexString() == string(id) {
				date := &upnpav.Date{Time: utils.Val(torrent.CreatedAt)}
				if torrent.UpdatedAt != nil {
					date = &upnpav.Date{Time: *torrent.UpdatedAt}
				}

				var containerIcon *upnpav.URL
				if torrent.Poster != nil && *torrent.Poster != "" {
					containerIcon = cd.posterURI(*torrent.Poster)
				}

				container := upnpav.Container{
					ID:         id,
					Parent:     allTorrentsContainerID,
					Title:      torrent.Name,
					Class:      upnpav.StorageFolder,
					Restricted: true,
					Searchable: true,
					Date:       date,
					Icon:       containerIcon,
					ChildCount: len(getMediaFiles(torrent.Files)),
				}
				return &upnpav.DIDLLite{
					Containers: []upnpav.Container{container},
				}, nil
			}
		}
		return nil, contentdirectory.ErrNoSuchObject
	}

	return &upnpav.DIDLLite{
		Containers: []upnpav.Container{
			{
				ID:         id,
				Parent:     upnpav.ObjectID(parentID),
				Title:      title,
				Class:      upnpav.StorageFolder,
				Restricted: true,
				Searchable: true,
				ChildCount: childCount,
			},
		},
	}, nil
}

func (cd *ContentDirectory) browseItemMetadata(_ context.Context, id upnpav.ObjectID) (*upnpav.DIDLLite, error) {
	parts := strings.Split(string(id), "/")
	if len(parts) != 2 {
		return nil, contentdirectory.ErrNoSuchObject
	}

	hashStr := parts[0]
	fileIndex, err := strconv.Atoi(parts[1])
	if err != nil || fileIndex < 0 {
		return nil, contentdirectory.ErrNoSuchObject
	}

	ih, err := utils.HashFromHexString(hashStr)
	if err != nil {
		return nil, contentdirectory.ErrNoSuchObject
	}

	torrent, err := cd.db.GetTorrent(ih)
	if err != nil {
		return nil, contentdirectory.ErrNoSuchObject
	}

	mediaFiles := getMediaFiles(torrent.Files)
	if fileIndex >= len(mediaFiles) {
		return nil, contentdirectory.ErrNoSuchObject
	}

	file := mediaFiles[fileIndex]

	class, err := upnpav.ClassForMIMEType(mime.TypeByExtension(path.Ext(file.Path)))
	if err != nil {
		return nil, upnpav.ErrActionFailed
	}

	var albumArtURIs []string
	var itemIcon *upnpav.URL

	if torrent.Poster != nil && *torrent.Poster != "" {
		if pURI := cd.posterURI(*torrent.Poster); pURI != nil {
			albumArtURIs = []string{pURI.String()}
			itemIcon = pURI
		}
	}

	if itemIcon == nil {
		if iURI := cd.iconURI(file.Path); iURI != nil {
			albumArtURIs = []string{iURI.String()}
			itemIcon = iURI
		}
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

	item := upnpav.Item{
		ID:           id,
		Parent:       upnpav.ObjectID(torrent.Hash.HexString()),
		Title:        file.Name,
		Class:        class,
		Restricted:   true,
		Searchable:   true,
		Icon:         itemIcon,
		AlbumArtURIs: albumArtURIs,
		Resources:    resources,
	}

	return &upnpav.DIDLLite{
		Items: []upnpav.Item{item},
	}, nil
}

func (cd *ContentDirectory) buildTorrentsDIDL(_ context.Context, parentID upnpav.ObjectID, torrents []*database.Torrent, startingIndex, requestedCount uint) (*upnpav.DIDLLite, uint, error) {
	var validTorrents []*database.Torrent
	for _, torrent := range torrents {
		if !hasMediaFiles(torrent.Files) {
			continue
		}
		validTorrents = append(validTorrents, torrent)
	}

	if parentID == allTorrentsContainerID {
		sort.SliceStable(validTorrents, func(i, j int) bool {
			return validTorrents[i].Name < validTorrents[j].Name
		})
	}

	totalMatches := uint(len(validTorrents))

	if startingIndex >= uint(len(validTorrents)) {
		return &upnpav.DIDLLite{}, totalMatches, nil
	}

	end := int(startingIndex) + int(requestedCount)
	if end > len(validTorrents) || requestedCount == 0 {
		end = len(validTorrents)
	}
	pagedTorrents := validTorrents[startingIndex:end]

	didl := &upnpav.DIDLLite{}
	for _, torrent := range pagedTorrents {
		date := &upnpav.Date{Time: utils.Val(torrent.CreatedAt)}
		if torrent.UpdatedAt != nil {
			date = &upnpav.Date{Time: *torrent.UpdatedAt}
		}

		var containerIcon *upnpav.URL
		if torrent.Poster != nil && *torrent.Poster != "" {
			containerIcon = cd.posterURI(*torrent.Poster)
		}

		container := upnpav.Container{
			ID:         upnpav.ObjectID(torrent.Hash.HexString()),
			Parent:     parentID,
			Title:      torrent.Name,
			Class:      upnpav.StorageFolder,
			Restricted: true,
			Searchable: true,
			Date:       date,
			Icon:       containerIcon,
			ChildCount: len(getMediaFiles(torrent.Files)),
		}
		didl.Containers = append(didl.Containers, container)
	}

	return didl, totalMatches, nil
}

func (cd *ContentDirectory) browseTorrent(_ context.Context, torrentHash upnpav.ObjectID, startingIndex, requestedCount uint) (*upnpav.DIDLLite, uint, error) {
	ih, err := utils.HashFromHexString(string(torrentHash))
	if err != nil {
		return nil, 0, err
	}
	torrent, err := cd.db.GetTorrent(ih)
	if err != nil {
		return nil, 0, err
	}

	mediaFiles := getMediaFiles(torrent.Files)
	totalMatches := uint(len(mediaFiles))

	if startingIndex >= uint(len(mediaFiles)) {
		return &upnpav.DIDLLite{}, totalMatches, nil
	}

	end := int(startingIndex) + int(requestedCount)
	if end > len(mediaFiles) || requestedCount == 0 {
		end = len(mediaFiles)
	}
	pagedFiles := mediaFiles[startingIndex:end]

	didl := &upnpav.DIDLLite{}
	for i, file := range pagedFiles {
		realIndex := int(startingIndex) + i

		class, err := upnpav.ClassForMIMEType(mime.TypeByExtension(path.Ext(file.Path)))
		if err != nil {
			continue
		}

		var albumArtURIs []string
		var itemIcon *upnpav.URL

		if torrent.Poster != nil && *torrent.Poster != "" {
			if pURI := cd.posterURI(*torrent.Poster); pURI != nil {
				albumArtURIs = []string{pURI.String()}
				itemIcon = pURI
			}
		}

		if itemIcon == nil {
			if iURI := cd.iconURI(file.Path); iURI != nil {
				albumArtURIs = []string{iURI.String()}
				itemIcon = iURI
			}
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

		item := upnpav.Item{
			ID:           upnpav.ObjectID(fmt.Sprintf("%s/%d", torrent.Hash.HexString(), realIndex)),
			Parent:       upnpav.ObjectID(torrent.Hash.HexString()),
			Title:        file.Name,
			Class:        class,
			Restricted:   true,
			Searchable:   true,
			Icon:         itemIcon,
			AlbumArtURIs: albumArtURIs,
			Resources:    resources,
		}
		didl.Items = append(didl.Items, item)
	}

	return didl, totalMatches, nil
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

func (cd *ContentDirectory) posterURI(poster string) *upnpav.URL {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	if cd.baseURL == nil || poster == "" {
		return nil
	}

	posterURL := *cd.baseURL
	posterURL.Path = path.Join(cd.postersPath, poster)
	return &upnpav.URL{URL: posterURL}
}

func (cd *ContentDirectory) iconURI(filepath string) *upnpav.URL {
	cd.mu.RLock()
	defer cd.mu.RUnlock()
	if cd.baseURL == nil {
		return nil
	}

	mediaType := strings.SplitN(mime.TypeByExtension(path.Ext(filepath)), "/", 2)[0]
	iconFilename := fmt.Sprintf("%sfile-128x128.png", mediaType)

	iconURL := *cd.baseURL
	iconURL.Path = path.Join(iconURL.Path, "/icons/media", iconFilename)
	return &upnpav.URL{URL: iconURL}
}

func getRecentlyAddedTorrents(allTorrents []*database.Torrent) []*database.Torrent {
	torrents := make([]*database.Torrent, len(allTorrents))
	copy(torrents, allTorrents)

	sort.Slice(torrents, func(i, j int) bool {
		return utils.Val(torrents[i].CreatedAt).After(utils.Val(torrents[j].CreatedAt))
	})

	if len(torrents) > recentlyItemsCount {
		return torrents[:recentlyItemsCount]
	}

	return torrents
}

func getRecentlyViewedTorrents(allTorrents []*database.Torrent) []*database.Torrent {
	type viewedTorrent struct {
		torrent  *database.Torrent
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

	result := make([]*database.Torrent, len(viewedTorrents))
	for i, wt := range viewedTorrents {
		result[i] = wt.torrent
	}

	return result
}

func hasMediaFiles(files []api.TorrentFile) bool {
	return slices.ContainsFunc(files, isMediaFile)
}

func isMediaFile(file api.TorrentFile) bool {
	if !media.IsAudioOrVideo(file.Path) && !media.IsImage(file.Path) {
		return false
	}
	_, err := upnpav.ClassForMIMEType(mime.TypeByExtension(path.Ext(file.Path)))
	return err == nil
}

func getMediaFiles(files []api.TorrentFile) []api.TorrentFile {
	sortedFiles := make([]api.TorrentFile, len(files))
	copy(sortedFiles, files)
	slices.SortFunc(sortedFiles, func(a, b api.TorrentFile) int {
		return strings.Compare(a.Path, b.Path)
	})

	var mediaFiles []api.TorrentFile
	for _, file := range sortedFiles {
		if isMediaFile(file) {
			mediaFiles = append(mediaFiles, file)
		}
	}
	return mediaFiles
}
