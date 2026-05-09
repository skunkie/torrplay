// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"embed"
	"fmt"
	"image"
	_ "image/png"
	"io/fs"
	"mime"
	"net/url"
	"path"
	"path/filepath"
	"slices"

	"github.com/ethulhu/helix/upnp"
)

//go:embed icons/*/*.png
var iconsFS embed.FS

func deviceIcons(fsys fs.FS, dir string, baseURL *url.URL) ([]upnp.Icon, error) {
	files, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("failed to read icon directory: %w", err)
	}

	icons := make([]upnp.Icon, 0, len(files))
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		iconPath := path.Join(dir, file.Name())
		f, err := fsys.Open(iconPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open icon file: %w", err)
		}
		defer f.Close()

		img, _, err := image.Decode(f)
		if err != nil {
			return nil, fmt.Errorf("failed to decode image: %w", err)
		}

		icons = append(icons, upnp.Icon{
			Width:    img.Bounds().Dx(),
			Height:   img.Bounds().Dy(),
			Depth:    24,
			MIMEType: mime.TypeByExtension(filepath.Ext(file.Name())),
			URL:      path.Join(baseURL.Path, iconPath),
		})
	}

	slices.SortFunc(icons, func(a, b upnp.Icon) int {
		if a.Width+a.Height > b.Width+b.Height {
			return 1
		} else if a.Width+a.Height < b.Width+b.Height {
			return -1
		}

		return 0
	})

	return icons, nil
}
