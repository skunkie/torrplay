// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/torrplay/torrplay/internal/api"
	"github.com/torrplay/torrplay/internal/metadata"
	"github.com/torrplay/torrplay/pkg/torrplay"
)

var (
	defaultDataDir = func() string {
		configDir, err := os.UserConfigDir()
		if err != nil {
			log.Printf("could not get user config dir: %v, using current directory", err)
			return "."
		}
		return filepath.Join(configDir, "TorrPlay")
	}()
	dataDir = flag.String("data-dir", defaultDataDir, "directory for storing configuration files")
	ipAddr  = flag.String("ipaddr", "0.0.0.0", "IP address to listen on")
	port    = flag.Int("port", -1, "port to listen on, overrides settings")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		log.Fatalf("application returned an error: %v", err)
	}
}

func runApp(ctx context.Context) error {
	app, err := torrplay.New(*dataDir, *ipAddr, *port)
	if err != nil {
		return fmt.Errorf("failed to initialize TorrPlay: %w", err)
	}

	app.Start()

	<-ctx.Done()
	app.Stop()

	return nil
}

func runMetadataTool() error {
	mdCmd := flag.NewFlagSet("metadata", flag.ExitOnError)
	mdProvider := mdCmd.String("provider", "tvdb", "Metadata provider (e.g., tvdb)")
	backupApiKey := mdCmd.String("api-key", "", "API key for fetching metadata")
	backupFile := mdCmd.String("backup", "torrplay.backup", "Path to the backup file")
	outputFile := mdCmd.String("output", "", "Path to the output file. If not provided, a new file will be created with the .updated suffix")
	categoryOpt := mdCmd.Bool("category", false, "Set category to Movies or Series")
	languageOpt := mdCmd.String("language", "", "Language for metadata (3-letter code)")
	posterOpt := mdCmd.Bool("poster", false, "Update poster")
	titleOpt := mdCmd.Bool("title", false, "Update title")

	mdCmd.Parse(os.Args[2:])

	if (*posterOpt || *titleOpt) && *backupApiKey == "" {
		return fmt.Errorf("api-key is required when updating posters or updating titles")
	}

	inputFile, err := os.Open(*backupFile)
	if err != nil {
		return fmt.Errorf("failed to open backup file: %w", err)
	}
	defer inputFile.Close()

	var backupData, updatedBackupData api.Backup
	if err := json.NewDecoder(inputFile).Decode(&backupData); err != nil {
		return fmt.Errorf("failed to parse backup file: %w", err)
	}

	if *posterOpt || *categoryOpt || *titleOpt {
		var provider metadata.Provider
		var err error
		switch *mdProvider {
		case "tvdb":
			provider, err = metadata.NewTVDBClient(*backupApiKey)
			if err != nil {
				return fmt.Errorf("failed to create tvdb client: %w", err)
			}
		default:
			return fmt.Errorf("unknown metadata provider: %s", *mdProvider)
		}

		opts := metadata.Options{
			Category: *categoryOpt,
			Language: *languageOpt,
			Poster:   *posterOpt,
			Title:    *titleOpt,
		}

		updatedBackupData, err = provider.UpdateMetadata(backupData, opts)
		if err != nil {
			return fmt.Errorf("failed to fetch metadata: %w", err)
		}
	} else {
		log.Println("skipping metadata update because no metadata options were enabled")
		return nil
	}

	outputFilename := *outputFile
	if outputFilename == "" {
		outputFilename = *backupFile + ".updated"
	}

	outputFileHandle, err := os.Create(outputFilename)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFileHandle.Close()

	encoder := json.NewEncoder(outputFileHandle)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(&updatedBackupData); err != nil {
		return fmt.Errorf("failed to write updated backup file: %w", err)
	}

	log.Printf("successfully processed backup, new backup file created at: %s", outputFilename)
	return nil
}
