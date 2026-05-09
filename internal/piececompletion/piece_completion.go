// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package piececompletion

import (
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	_ "modernc.org/sqlite"
)

const (
	batchSize    = 1000
	batchTimeout = 500 * time.Millisecond
)

var _ DeletablePieceCompletion = (*PieceCompletion)(nil)

// DeletablePieceCompletion extends the storage.PieceCompletion with a method to delete entries for a torrent.
type DeletablePieceCompletion interface {
	storage.PieceCompletion
	Delete(infoHash metainfo.Hash) error
	Close() error
}

type PieceCompletion struct {
	batch    map[metainfo.PieceKey]bool
	batchMu  sync.RWMutex
	db       *sql.DB
	logger   *slog.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
	updates  chan pieceUpdate
	wg       sync.WaitGroup
}

type pieceUpdate struct {
	pk       metainfo.PieceKey
	complete bool
}

func New(dir string, logger *slog.Logger) (DeletablePieceCompletion, error) {
	dbPath := filepath.Join(dir, ".torrent.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_txlock=immediate", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db: %w", err)
	}

	_, err = db.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		return nil, fmt.Errorf("failed to enable wal mode: %w", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS piece_completion (
			infohash TEXT,
			piece_index INTEGER,
			PRIMARY KEY (infohash, piece_index)
		);
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to create table: %w", err)
	}

	pc := &PieceCompletion{
		batch:   make(map[metainfo.PieceKey]bool),
		db:      db,
		logger:  logger,
		stopCh:  make(chan struct{}),
		updates: make(chan pieceUpdate, batchSize),
	}

	pc.wg.Add(1)
	go pc.worker()

	return pc, nil
}

func (pc *PieceCompletion) worker() {
	defer pc.wg.Done()
	ticker := time.NewTicker(batchTimeout)
	defer ticker.Stop()
	updates := make([]pieceUpdate, 0, batchSize)

	for {
		select {
		case <-pc.stopCh:
			pc.flush(updates)
			return
		case update := <-pc.updates:
			updates = append(updates, update)
			if len(updates) >= batchSize {
				pc.flush(updates)
				updates = updates[:0]
			}
		case <-ticker.C:
			if len(updates) > 0 {
				pc.flush(updates)
				updates = updates[:0]
			}
		}
	}
}

func (pc *PieceCompletion) flush(updates []pieceUpdate) {
	if len(updates) == 0 {
		return
	}

	tx, err := pc.db.Begin()
	if err != nil {
		pc.logger.Error("failed to begin transaction", slog.Any("error", err))
		return
	}
	defer func() {
		_ = tx.Rollback()
	}()

	insertStmt, err := tx.Prepare("INSERT OR IGNORE INTO piece_completion (infohash, piece_index) VALUES (?, ?)")
	if err != nil {
		pc.logger.Error("failed to prepare insert statement", slog.Any("error", err))
		return
	}
	defer insertStmt.Close()

	deleteStmt, err := tx.Prepare("DELETE FROM piece_completion WHERE infohash = ? AND piece_index = ?")
	if err != nil {
		pc.logger.Error("failed to prepare delete statement", slog.Any("error", err))
		return
	}
	defer deleteStmt.Close()

	for _, update := range updates {
		if update.complete {
			_, err = insertStmt.Exec(update.pk.InfoHash.HexString(), update.pk.Index)
		} else {
			_, err = deleteStmt.Exec(update.pk.InfoHash.HexString(), update.pk.Index)
		}
		if err != nil {
			pc.logger.Error("failed to execute statement", slog.Any("error", err), slog.Int("piece_index", update.pk.Index), slog.String("infohash", update.pk.InfoHash.HexString()))
			continue
		}
	}

	if err = tx.Commit(); err != nil {
		pc.logger.Error("failed to commit transaction", slog.Any("error", err))
		return
	}

	pc.batchMu.Lock()
	defer pc.batchMu.Unlock()
	for _, u := range updates {
		if val, ok := pc.batch[u.pk]; ok && val == u.complete {
			delete(pc.batch, u.pk)
		}
	}
}

func (pc *PieceCompletion) Get(pk metainfo.PieceKey) (storage.Completion, error) {
	pc.batchMu.RLock()
	complete, ok := pc.batch[pk]
	pc.batchMu.RUnlock()

	if ok {
		return storage.Completion{Complete: complete, Ok: true}, nil
	}

	var count int
	err := pc.db.QueryRow(
		"SELECT COUNT(*) FROM piece_completion WHERE infohash = ? AND piece_index = ?",
		pk.InfoHash.HexString(),
		pk.Index,
	).Scan(&count)

	if err != nil {
		return storage.Completion{}, fmt.Errorf("failed to get piece completion: %w", err)
	}

	return storage.Completion{Complete: count > 0, Ok: true}, nil
}

func (pc *PieceCompletion) Set(pk metainfo.PieceKey, complete bool) error {
	pc.batchMu.Lock()
	pc.batch[pk] = complete
	pc.batchMu.Unlock()

	select {
	case pc.updates <- pieceUpdate{pk: pk, complete: complete}:
		return nil
	case <-pc.stopCh:
		return fmt.Errorf("piece completion is closed")
	}
}

func (pc *PieceCompletion) Delete(infoHash metainfo.Hash) error {
	_, err := pc.db.Exec(
		"DELETE FROM piece_completion WHERE infohash = ?",
		infoHash.HexString(),
	)
	if err != nil {
		return fmt.Errorf("failed to delete piece completion for %s: %w", infoHash.HexString(), err)
	}
	return nil
}

func (pc *PieceCompletion) Close() error {
	pc.stopOnce.Do(func() {
		close(pc.stopCh)
	})
	pc.wg.Wait()
	return pc.db.Close()
}
