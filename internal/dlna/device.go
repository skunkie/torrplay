// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package dlna

import (
	"net/http"

	"github.com/ethulhu/helix/upnp"
	"github.com/ethulhu/helix/upnpav"
	"github.com/ethulhu/helix/upnpav/connectionmanager"
	"github.com/ethulhu/helix/upnpav/contentdirectory"
)

// NewDevice creates a new UPnP device for the TorrPlay media server.
// It sets up the device metadata and registers the ContentDirectory and ConnectionManager services.
func NewDevice(friendlyName, udn string, icons []upnp.Icon, cd *ContentDirectory) *upnp.Device {
	device := &upnp.Device{
		Name:             friendlyName,
		UDN:              udn,
		DeviceType:       contentdirectory.DeviceType,
		Icons:            icons,
		Manufacturer:     "TorrPlay",
		ManufacturerURL:  "https://github.com/torrplay/torrplay",
		ModelDescription: "TorrPlay",
		ModelName:        "TorrPlay",
		ModelNumber:      "00",
		ModelURL:         "https://github.com/torrplay/torrplay",
		SerialNumber:     "00000000",
		PresentationURL:  "/",
	}

	device.Handle(contentdirectory.Version1, contentdirectory.ServiceID, contentdirectory.SCPD, contentdirectory.SOAPHandler{Interface: cd})
	device.Handle(connectionmanager.Version1, connectionmanager.ServiceID, connectionmanager.SCPD, nil) // We don't need a real connection manager.

	return device
}

// AddHeader adds the DLNA content features header to the response if requested.
func AddHeader(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("getContentFeatures.dlna.org") != "" {
		w.Header().Set("contentFeatures.dlna.org", upnpav.ContentFeatures)
	}
}
