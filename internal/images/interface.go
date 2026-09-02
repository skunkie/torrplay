// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package images

import (
	"context"
	"net/http"
)

type ServiceInterface interface {
	Delete(id *string) error
	DownloadImageData(ctx context.Context, url string) ([]byte, error)
	Get(id string) ([]byte, error)
	ListIDs() ([]string, error)
	SaveData(data []byte) (*string, error)
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}
