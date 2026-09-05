// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package controller

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/httpserver"
)

func TestGetSystemInfoReturnsDeployment(t *testing.T) {
	t.Setenv("TORRPLAY_DEPLOYMENT", "native")
	controller := &Controller{
		httpServer: httpserver.NewServer(http.NewServeMux(), "127.0.0.1:8090", slog.Default()),
		startedAt:  time.Now(),
	}
	recorder := httptest.NewRecorder()

	controller.GetSystemInfo(recorder, httptest.NewRequest(http.MethodGet, "/api/system/info", http.NoBody))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GetSystemInfo status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var info api.SystemInfo
	if err := json.NewDecoder(recorder.Body).Decode(&info); err != nil {
		t.Fatalf("decode system info: %v", err)
	}
	if info.Deployment != api.SystemInfoDeploymentNative {
		t.Errorf("deployment = %q, want %q", info.Deployment, api.SystemInfoDeploymentNative)
	}
}

func TestSystemOperatingSystem(t *testing.T) {
	tests := map[string]api.SystemInfoOs{
		"darwin":  api.SystemInfoOsMacos,
		"windows": api.SystemInfoOsWindows,
		"linux":   api.SystemInfoOsLinux,
		"android": api.SystemInfoOsAndroid,
		"ios":     api.SystemInfoOsIos,
		"plan9":   api.SystemInfoOsUnknown,
	}
	for input, expected := range tests {
		if actual := systemOperatingSystem(input); actual != expected {
			t.Errorf("systemOperatingSystem(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestSystemArchitecture(t *testing.T) {
	tests := map[string]api.SystemInfoArchitecture{
		"amd64": api.SystemInfoArchitectureX64,
		"arm64": api.SystemInfoArchitectureArm64,
		"arm":   api.SystemInfoArchitectureArmv7,
		"386":   api.SystemInfoArchitectureX86,
		"mips":  api.SystemInfoArchitectureUnknown,
	}
	for input, expected := range tests {
		if actual := systemArchitecture(input); actual != expected {
			t.Errorf("systemArchitecture(%q) = %q, want %q", input, actual, expected)
		}
	}
}

func TestSystemDeployment(t *testing.T) {
	tests := []struct {
		name               string
		configured         string
		dockerMarkerExists bool
		expected           api.SystemInfoDeployment
	}{
		{
			name:       "explicit container",
			configured: "container",
			expected:   api.SystemInfoDeploymentContainer,
		},
		{
			name:               "explicit native overrides Docker marker",
			configured:         "native",
			dockerMarkerExists: true,
			expected:           api.SystemInfoDeploymentNative,
		},
		{
			name:               "valid value ignores whitespace and case",
			configured:         " Container ",
			dockerMarkerExists: false,
			expected:           api.SystemInfoDeploymentContainer,
		},
		{
			name:               "Docker marker fallback",
			dockerMarkerExists: true,
			expected:           api.SystemInfoDeploymentContainer,
		},
		{
			name:               "invalid value uses Docker marker fallback",
			configured:         "unknown",
			dockerMarkerExists: true,
			expected:           api.SystemInfoDeploymentContainer,
		},
		{
			name:     "default native",
			expected: api.SystemInfoDeploymentNative,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if actual := systemDeployment(test.configured, test.dockerMarkerExists); actual != test.expected {
				t.Errorf("systemDeployment(%q, %t) = %q, want %q", test.configured, test.dockerMarkerExists, actual, test.expected)
			}
		})
	}
}
