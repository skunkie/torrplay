// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package images

import (
	"context"
	"errors"
	"net/http"
)

var _ ServiceInterface = (*Unimplemented)(nil)

var ErrUnimplemented = errors.New("unimplemented")

// Unimplemented is an ImageService implementation that returns ErrUnimplemented for all methods.
// It is used for embedding in other implementations to ensure forward compatibility.
type Unimplemented struct{}

func (Unimplemented) Delete(_ *string) error { return ErrUnimplemented }
func (Unimplemented) DownloadImageData(_ context.Context, url string) ([]byte, error) {
	return nil, ErrUnimplemented
}
func (Unimplemented) Get(_ string) ([]byte, error)       { return nil, ErrUnimplemented }
func (Unimplemented) ListIDs() ([]string, error)         { return nil, ErrUnimplemented }
func (Unimplemented) SaveData(_ []byte) (*string, error) { return nil, ErrUnimplemented }
func (Unimplemented) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, ErrUnimplemented.Error(), http.StatusNotImplemented)
}
