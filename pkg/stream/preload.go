// SPDX-FileCopyrightText: 2026 TorrPlay
//
// SPDX-License-Identifier: MIT

package stream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// PreloadState is the lifecycle state of a torrent's preload.
type PreloadState uint8

const (
	// PreloadQueued waits for a free preload slot or for memory to reserve.
	PreloadQueued PreloadState = iota + 1
	// PreloadRunning downloads the pieces of the preload's ranges.
	PreloadRunning
	// PreloadReady holds every piece of the preload's ranges.
	PreloadReady
	// PreloadFailed could not reserve memory with nothing left to wait for.
	PreloadFailed
	// PreloadEvicted gave up its memory to another preload or to a smaller
	// readahead budget.
	PreloadEvicted
)

// String returns the state's lowercase name, or "unknown" for an invalid state.
func (s PreloadState) String() string {
	switch s {
	case PreloadQueued:
		return "queued"
	case PreloadRunning:
		return "running"
	case PreloadReady:
		return "ready"
	case PreloadFailed:
		return "failed"
	case PreloadEvicted:
		return "evicted"
	default:
		return "unknown"
	}
}

// PreloadStatus reports a torrent's preload.
type PreloadStatus struct {
	// CompletedBytes is the number of target bytes held in complete pieces.
	CompletedBytes int64
	// FileIndex is the preloaded file's index within its torrent.
	FileIndex int
	// FilePath is the preloaded file's path within its torrent.
	FilePath string
	// State is the preload's lifecycle state.
	State PreloadState
	// TargetBytes is the size of the preload's head and tail ranges.
	TargetBytes int64
}

// ErrPreloadDoesNotFit is returned by Preload when not even one piece of the
// file fits the preload memory limit.
var ErrPreloadDoesNotFit = errors.New("preload does not fit the memory limit")

const (
	defaultPreloadReadyTTL = 5 * time.Minute

	// maxConcurrentPreloads caps preloads that are downloading. Preloads share
	// one priority level and download in rarity order, so running fewer at a
	// time lets each become ready sooner. Raising this does not shrink an
	// individual preload on a machine with memory to spare.
	maxConcurrentPreloads = 2

	// maxPreloadBytes bounds a single preload. It is a startup-latency limit,
	// not a memory one: no amount of configured memory makes anyone willing to
	// wait longer for playback to begin, so it deliberately does not scale
	// with the readahead budget. Aggregate preload memory is capped by the
	// preload share of the budget at reservation time, not here.
	maxPreloadBytes = 32 << 20

	// minSharedPreloadBytes is the smallest per-preload share worth running
	// preloads concurrently for: a default head and tail boundary. Below it, a
	// single preload takes the whole capacity instead.
	minSharedPreloadBytes = 2 * int64(DefaultFileBoundaryBytes)
)

// preload caches the head and tail of one file so that playback of the file
// starts without waiting for its container metadata. It downloads without a
// reader, by claiming its pieces at PiecePriorityHigh: below the pieces that
// playback readers claim or read ahead, and above background downloads. A
// memory-storage preload reserves its pieces in the protection budget and
// protects them from eviction until it is stopped. The pool guards every
// field with Pool.mu.
type preload struct {
	// cancel stops the preload's watcher goroutine. It is nil while queued
	// and after the preload stops.
	cancel context.CancelFunc
	// claimed holds the pieces this preload claims at PiecePriorityHigh.
	claimed        []int
	completedBytes int64
	file           *torrent.File
	fileIndex      int
	// finishedAt is when the preload failed or was evicted.
	finishedAt time.Time
	// headEnd, tailStart, and tailEnd are the file-relative byte ranges
	// [0, headEnd) and [tailStart, tailEnd) the preload caches. An empty tail
	// range caches only the head.
	headEnd, tailStart, tailEnd int64
	infoHash                    metainfo.Hash
	mode                        StorageMode
	pieces                      []int
	// read reports that a reader of the preload's file was acquired since the
	// preload was requested. Once its file has no reader left, a read preload
	// has served its purpose and is released.
	read bool
	// readyAt is when the preload became ready. The ready TTL of a preload
	// whose file has not been read runs from it.
	readyAt     time.Time
	reservation preloadReservation
	reserved    bool
	state       PreloadState
	targetBytes int64
}

// preloadReservation is the protection budget held by a memory-storage
// preload, the file and pieces it protects, and whether it protects both that
// file's head and tail.
type preloadReservation struct {
	bytes            int64
	coversBoundaries bool
	filePath         string
	// headStart, headEnd, tailStart, and tailEnd are the inclusive piece
	// ranges protected from eviction while the reservation is held.
	headStart, headEnd, tailStart, tailEnd int
	// headID and tailID identify the reservation's protected ranges in the
	// registry. They are drawn from the reader ID sequence, so they never
	// collide with a reader's own range.
	headID, tailID uint64
}

// Preload starts caching the head and tail of file, replacing any preload of
// another file of the same torrent, and returns the preload's status. A
// request for the file already being preloaded, or already cached, returns
// that preload unchanged. Memory-storage preloads reserve their whole pieces
// within the preload share of the readahead budget; file-storage preloads are
// written to disk and reserve nothing. Preloads never pause for playback:
// playback readers claim and read ahead at higher priorities, so preloads
// download with the bandwidth playback leaves. Preload returns
// ErrPreloadDoesNotFit, and holds no preload for the torrent, when not even
// one piece fits the memory limit.
func (p *Pool) Preload(file *torrent.File, mode StorageMode) (PreloadStatus, error) {
	if file == nil || file.Torrent() == nil || file.Torrent().Info() == nil {
		return PreloadStatus{}, ErrInvalidFile
	}
	if mode != MemoryStorage && mode != FileStorage {
		return PreloadStatus{}, fmt.Errorf("%w: %d", ErrInvalidStorageMode, mode)
	}
	tor := file.Torrent()
	infoHash := tor.InfoHash()

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return PreloadStatus{}, ErrPoolClosed
	}
	if current := p.preloads[infoHash]; current != nil && current.file == file && current.mode == mode && current.state <= PreloadReady {
		return current.status(), nil
	}

	pl, ok := p.planPreloadLocked(file, mode)
	p.removePreloadLocked(infoHash)
	if !ok {
		p.dispatchPreloadsLocked()
		return PreloadStatus{}, ErrPreloadDoesNotFit
	}
	pl.completedBytes, _ = pl.progress()
	pl.read = p.fileHasReadersLocked(file)
	tor.AllowDataDownload()
	p.preloads[infoHash] = pl
	p.preloadQueue = append(p.preloadQueue, pl)
	p.dispatchPreloadsLocked()
	p.logger.Debug("queued preload",
		slog.String("hash", infoHash.HexString()),
		slog.String("file", file.Path()),
		slog.Int64("targetBytes", pl.targetBytes),
		slog.String("state", pl.state.String()))
	return pl.status(), nil
}

// PreloadStatus returns the status of a torrent's preload. A failed or
// evicted preload keeps reporting its final state for the ready TTL. It
// returns false when the torrent has no preload, including after its torrent
// closed.
func (p *Pool) PreloadStatus(infoHash metainfo.Hash) (PreloadStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pl := p.preloads[infoHash]
	if pl == nil {
		return PreloadStatus{}, false
	}
	if torrentClosed(pl.file) {
		p.removePreloadLocked(infoHash)
		p.dispatchPreloadsLocked()
		return PreloadStatus{}, false
	}
	return pl.status(), true
}

// CancelPreload stops a torrent's preload and releases its memory and
// protection. It is a no-op for a torrent without a preload.
func (p *Pool) CancelPreload(infoHash metainfo.Hash) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.preloads[infoHash]; !ok {
		return
	}
	p.removePreloadLocked(infoHash)
	p.dispatchPreloadsLocked()
}

// PreloadCapacity returns the total bytes that preload reservations may hold
// under the current readahead budget. The remainder of the budget is kept for
// playback readers.
func (p *Pool) PreloadCapacity() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return preloadProtectionCapacity(p.readaheadBudget)
}

// status returns the preload's status. A ready preload reports its whole
// target as completed.
func (pl *preload) status() PreloadStatus {
	completed := pl.completedBytes
	if pl.state == PreloadReady {
		completed = pl.targetBytes
	}
	return PreloadStatus{
		CompletedBytes: completed,
		FileIndex:      pl.fileIndex,
		FilePath:       pl.file.Path(),
		State:          pl.state,
		TargetBytes:    pl.targetBytes,
	}
}

// progress returns the bytes of the preload's ranges held in complete pieces,
// and whether every piece of its ranges is complete. It reads piece states
// under the torrent client lock.
func (pl *preload) progress() (completedBytes int64, complete bool) {
	tor := pl.file.Torrent()
	states := make(map[int]bool, len(pl.pieces))
	pieceComplete := func(index int) bool {
		done, ok := states[index]
		if !ok {
			done = tor.PieceState(index).Complete
			states[index] = done
		}
		return done
	}
	pieceLength := tor.Info().PieceLength
	completedBytes = completedRangeBytes(pieceComplete, pieceLength, pl.file.Offset(), 0, pl.headEnd) +
		completedRangeBytes(pieceComplete, pieceLength, pl.file.Offset(), pl.tailStart, pl.tailEnd)
	for _, index := range pl.pieces {
		if !pieceComplete(index) {
			return completedBytes, false
		}
	}
	return completedBytes, true
}

// completedRangeBytes returns the bytes of the file-relative range
// [start, end) that lie in complete pieces of a file beginning at fileOffset.
func completedRangeBytes(pieceComplete func(int) bool, pieceLength, fileOffset, start, end int64) int64 {
	if end <= start || pieceLength <= 0 {
		return 0
	}
	begin := fileOffset + start
	finish := fileOffset + end
	var total int64
	for index := begin / pieceLength; index*pieceLength < finish; index++ {
		if pieceComplete(int(index)) {
			total += min(finish, (index+1)*pieceLength) - max(begin, index*pieceLength)
		}
	}
	return total
}

// planPreloadLocked sizes a preload of file. The head starts at the file's
// beginning and the tail, when the file is large enough to have one, covers at
// least a default boundary or one piece at its end, so container metadata at
// either end is cached. Memory-storage preloads are trimmed to whole pieces
// within their share of the preload capacity. It returns false when not even
// one piece fits. Must be called with p.mu held.
func (p *Pool) planPreloadLocked(file *torrent.File, mode StorageMode) (*preload, bool) {
	info := file.Torrent().Info()
	// File-storage preloads are written to disk and reserve no memory, so only
	// the startup-latency ceiling bounds them.
	limit := int64(maxPreloadBytes)
	if mode == MemoryStorage {
		limit = preloadMemoryLimit(preloadProtectionCapacity(p.readaheadBudget))
	}
	budget := min(file.Length(), limit)
	if budget <= 0 {
		return nil, false
	}

	startend := max(int64(DefaultFileBoundaryBytes), info.PieceLength)
	var headEnd, tailStart, tailEnd int64
	switch {
	case file.Length() <= startend, budget <= startend:
		headEnd = budget
	default:
		tailStart = file.Length() - startend
		tailEnd = file.Length()
		headEnd = min(budget-startend, tailStart)
	}
	if mode == MemoryStorage {
		// Fit against the memory limit rather than the budget, which is also
		// capped by the file length: a file smaller than one piece still
		// occupies the whole piece.
		headEnd, tailStart, tailEnd = fitPreloadRanges(file, limit, headEnd, tailStart, tailEnd)
	}
	headStart, headEndPiece, tailStartPiece, tailEndPiece, ok := FilePieceRanges(file, headEnd, tailStart, tailEnd)
	if !ok {
		return nil, false
	}
	return &preload{
		file:        file,
		fileIndex:   slices.Index(file.Torrent().Files(), file),
		headEnd:     headEnd,
		infoHash:    file.Torrent().InfoHash(),
		mode:        mode,
		pieces:      BoundaryPieces(headStart, headEndPiece, tailStartPiece, tailEndPiece),
		state:       PreloadQueued,
		tailEnd:     tailEnd,
		tailStart:   tailStart,
		targetBytes: headEnd + (tailEnd - tailStart),
		reservation: preloadReservation{
			bytes:            BoundaryPieceBytes(info, headStart, headEndPiece, tailStartPiece, tailEndPiece),
			coversBoundaries: preloadCoversBoundaries(file.Length(), headEnd, tailStart, tailEnd),
			filePath:         file.Path(),
			headStart:        headStart,
			headEnd:          headEndPiece,
			tailStart:        tailStartPiece,
			tailEnd:          tailEndPiece,
		},
	}, true
}

// preloadMemoryLimit returns the bytes one memory-storage preload may protect,
// given the total preload capacity. Preloads run concurrently with an even
// share each when that share still covers a default head and tail boundary;
// otherwise one preload uses the whole capacity and later requests wait for
// it, because splitting would leave each too small to be useful. The
// startup-latency ceiling applies either way.
func preloadMemoryLimit(capacity int64) int64 {
	limit := capacity / maxConcurrentPreloads
	if limit < minSharedPreloadBytes {
		limit = capacity
	}
	return max(min(limit, maxPreloadBytes), 0)
}

// fitPreloadRanges trims the head range [0, headEnd) and the tail range
// [tailStart, tailEnd) until the whole pieces they touch fit within budget.
// Storage protects and evicts complete pieces, so a range ending one byte into
// a piece costs that entire piece, and byte-sized ranges can exceed the
// preload's admission capacity. Each step drops the last piece of the head or
// the first piece of the tail, whichever range spans more pieces, keeping at
// least one head piece while a tail remains. A returned headEnd of zero means
// that not even one piece fits.
func fitPreloadRanges(file *torrent.File, budget, headEnd, tailStart, tailEnd int64) (int64, int64, int64) {
	info := file.Torrent().Info()
	pieceLength := max(info.PieceLength, 1)
	fileOffset := file.Offset()
	for headEnd > 0 {
		headStartPiece, headEndPiece, tailStartPiece, tailEndPiece, ok := FilePieceRanges(file, headEnd, tailStart, tailEnd)
		if !ok || BoundaryPieceBytes(info, headStartPiece, headEndPiece, tailStartPiece, tailEndPiece) <= budget {
			break
		}
		headPieces := headEndPiece - headStartPiece + 1
		tailPieces := tailEndPiece - tailStartPiece + 1
		if tailEnd > tailStart && (tailPieces > headPieces || headPieces == 1) {
			// Start the tail at the next piece boundary, dropping it when
			// nothing is left.
			tailStart = int64(tailStartPiece+1)*pieceLength - fileOffset
			if tailStart >= tailEnd {
				tailStart, tailEnd = 0, 0
			}
			continue
		}
		// End the head where its last piece begins; an empty head means that
		// the budget cannot hold a single piece.
		headEnd = max(int64(headEndPiece)*pieceLength-fileOffset, 0)
	}
	if headEnd <= 0 {
		return 0, 0, 0
	}
	return headEnd, tailStart, tailEnd
}

// preloadCoversBoundaries reports whether the head range [0, headEnd) and the
// tail range [tailStart, tailEnd) reach both ends of a file of fileLength
// bytes. A preload trimmed to part of its head leaves the tail to playback
// boundary protection; one whose head spans the whole file covers the tail as
// well.
func preloadCoversBoundaries(fileLength, headEnd, tailStart, tailEnd int64) bool {
	return headEnd > 0 && (headEnd >= fileLength || (tailEnd > tailStart && tailEnd >= fileLength))
}

// preloadProtectionCapacity returns the portion of the shared protection
// budget that preloads may reserve. The remainder is kept available for
// playback readers, so a newly acquired stream is never starved by held
// preloads.
func preloadProtectionCapacity(totalBudget int64) int64 {
	playbackReserve := min(totalBudget/2, 2*int64(DefaultFileBoundaryBytes))
	return max(totalBudget-playbackReserve, 0)
}

// dispatchPreloadsLocked starts queued preloads in FIFO order while fewer than
// maxConcurrentPreloads are running. A memory-storage preload first reserves
// its pieces, evicting ready preloads that no reader is using if it must. When
// its pieces do not fit, it waits for a running preload to become ready and
// evictable, or fails when none is running. Must be called with p.mu held.
func (p *Pool) dispatchPreloadsLocked() {
	for len(p.preloadQueue) > 0 && p.runningPreloadCountLocked() < maxConcurrentPreloads {
		pl := p.preloadQueue[0]
		if p.preloads[pl.infoHash] != pl || pl.state != PreloadQueued {
			p.preloadQueue = p.preloadQueue[1:]
			continue
		}
		if pl.mode == MemoryStorage && !p.reservePreloadLocked(pl) {
			if p.runningPreloadCountLocked() > 0 {
				return
			}
			p.preloadQueue = p.preloadQueue[1:]
			p.finishPreloadLocked(pl, PreloadFailed)
			p.logger.Warn("preload cannot reserve stream memory budget",
				slog.String("hash", pl.infoHash.HexString()),
				slog.String("file", pl.file.Path()),
				slog.Int64("bytes", pl.reservation.bytes))
			continue
		}
		p.preloadQueue = p.preloadQueue[1:]
		p.startPreloadLocked(pl)
	}
}

// runningPreloadCountLocked returns the number of running preloads. Must be
// called with p.mu held.
func (p *Pool) runningPreloadCountLocked() int {
	count := 0
	for _, pl := range p.preloads {
		if pl.state == PreloadRunning {
			count++
		}
	}
	return count
}

// startPreloadLocked claims a preload's pieces and starts its watcher. Must be
// called with p.mu held.
func (p *Pool) startPreloadLocked(pl *preload) {
	pl.state = PreloadRunning
	p.claimPreloadLocked(pl)
	ctx, cancel := context.WithCancel(context.Background())
	pl.cancel = cancel
	p.preloadWatchers.Go(func() { p.watchPreload(ctx, pl) })
	p.logger.Debug("started preload",
		slog.String("hash", pl.infoHash.HexString()),
		slog.String("file", pl.file.Path()))
}

// watchPreload follows the piece states of a preload's ranges for as long as
// the preload runs or is ready. It marks the preload ready once every piece is
// complete, and running again if storage evicts one of its pieces, and it
// removes the preload when its torrent closes.
func (p *Pool) watchPreload(ctx context.Context, pl *preload) {
	tor := pl.file.Torrent()
	// Subscribe before the first check, so no change between the check and
	// the wait is missed.
	sub := tor.SubscribePieceStateChanges()
	defer sub.Close()
	watched := make(map[int]struct{}, len(pl.pieces))
	for _, index := range pl.pieces {
		watched[index] = struct{}{}
	}

	for {
		completedBytes, complete := pl.progress()
		p.mu.Lock()
		if ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		p.updatePreloadLocked(pl, completedBytes, complete)
		p.mu.Unlock()

		if !waitForPieceChange(ctx, tor, sub.Values, watched) {
			if ctx.Err() == nil {
				p.mu.Lock()
				if p.preloads[pl.infoHash] == pl {
					p.removePreloadLocked(pl.infoHash)
					p.dispatchPreloadsLocked()
				}
				p.mu.Unlock()
			}
			return
		}
	}
}

// waitForPieceChange blocks until the state of a watched piece changes, and
// then drains changes already delivered so that a burst causes one check. It
// returns false when ctx ends or the torrent closes.
func waitForPieceChange(ctx context.Context, tor *torrent.Torrent, changes <-chan torrent.PieceStateChange, watched map[int]struct{}) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tor.Closed():
			return false
		case change, ok := <-changes:
			if !ok {
				return false
			}
			if _, relevant := watched[change.Index]; !relevant {
				continue
			}
			for {
				select {
				case _, ok := <-changes:
					if !ok {
						return false
					}
				default:
					return true
				}
			}
		}
	}
}

// updatePreloadLocked records a preload's progress. A running preload whose
// pieces are all complete becomes ready and gives up its priority claims; a
// ready preload that lost a piece runs again. Must be called with p.mu held.
func (p *Pool) updatePreloadLocked(pl *preload, completedBytes int64, complete bool) {
	if p.closed || p.preloads[pl.infoHash] != pl {
		return
	}
	pl.completedBytes = completedBytes
	switch {
	case pl.state == PreloadRunning && complete:
		pl.state = PreloadReady
		pl.readyAt = time.Now()
		p.unclaimPreloadLocked(pl)
		p.logger.Debug("preload ready",
			slog.String("hash", pl.infoHash.HexString()),
			slog.String("file", pl.file.Path()))
		// Playback that started and ended while the preload ran no longer
		// needs its cache.
		if pl.read && !p.fileHasReadersLocked(pl.file) {
			p.removePreloadLocked(pl.infoHash)
		}
		p.dispatchPreloadsLocked()
	case pl.state == PreloadReady && !complete:
		pl.state = PreloadRunning
		p.claimPreloadLocked(pl)
		p.logger.Debug("preload lost a piece and runs again",
			slog.String("hash", pl.infoHash.HexString()),
			slog.String("file", pl.file.Path()))
	}
}

// claimPreloadLocked claims every piece of a preload at PiecePriorityHigh.
// Must be called with p.mu held.
func (p *Pool) claimPreloadLocked(pl *preload) {
	planned := make([]prioritizedPiece, 0, len(pl.pieces))
	for _, index := range pl.pieces {
		planned = append(planned, prioritizedPiece{index: index, priority: torrent.PiecePriorityHigh})
	}
	tor := pl.file.Torrent()
	p.priorityMu.Lock()
	pl.claimed = p.replaceClaimsLocked(pl, tor, pl.claimed, tor, planned)
	p.priorityMu.Unlock()
}

// unclaimPreloadLocked removes a preload's priority claims. Must be called
// with p.mu held.
func (p *Pool) unclaimPreloadLocked(pl *preload) {
	if len(pl.claimed) == 0 {
		return
	}
	p.priorityMu.Lock()
	p.clearClaimsLocked(pl, pl.file.Torrent(), pl.claimed)
	p.priorityMu.Unlock()
	pl.claimed = nil
}

// reservePreloadLocked reserves a memory-storage preload's pieces and protects
// them from eviction, evicting ready preloads that no reader is using, oldest
// first, when the preload share is full. It returns false when the pieces do
// not fit even then. Must be called with p.mu held.
func (p *Pool) reservePreloadLocked(pl *preload) bool {
	for pl.reservation.bytes > preloadProtectionCapacity(p.readaheadBudget)-p.reservedPreloadBytesLocked() {
		if !p.evictIdlePreloadLocked() {
			return false
		}
	}
	p.nextID++
	pl.reservation.headID = p.nextID
	p.nextID++
	pl.reservation.tailID = p.nextID
	pl.reserved = true
	if p.cfg.Registry != nil {
		p.cfg.Registry.SetActiveRange(pl.infoHash, pl.reservation.headID, pl.reservation.headStart, pl.reservation.headEnd)
		p.cfg.Registry.SetActiveRange(pl.infoHash, pl.reservation.tailID, pl.reservation.tailStart, pl.reservation.tailEnd)
	}
	p.refreshReadaheadLocked(p.readaheadBudget)
	return true
}

// releasePreloadReservationLocked ends the eviction protection of a preload's
// pieces and returns its reservation to playback readers. Must be called with
// p.mu held.
func (p *Pool) releasePreloadReservationLocked(pl *preload) {
	if !pl.reserved {
		return
	}
	pl.reserved = false
	if p.cfg.Registry != nil {
		p.cfg.Registry.ClearActiveRange(pl.infoHash, pl.reservation.headID)
		p.cfg.Registry.ClearActiveRange(pl.infoHash, pl.reservation.tailID)
	}
	p.refreshReadaheadLocked(p.readaheadBudget)
}

// reservedPreloadBytesLocked returns the bytes all preloads reserve. Must be
// called with p.mu held.
func (p *Pool) reservedPreloadBytesLocked() int64 {
	var reserved int64
	for _, pl := range p.preloads {
		if pl.reserved {
			reserved += pl.reservation.bytes
		}
	}
	return reserved
}

// evictIdlePreloadLocked evicts the oldest ready preload, skipping preloads
// whose file is being read. It returns false when there is none. Must be
// called with p.mu held.
func (p *Pool) evictIdlePreloadLocked() bool {
	var victim *preload
	for _, pl := range p.preloads {
		if pl.state != PreloadReady || !pl.reserved || p.fileHasReadersLocked(pl.file) {
			continue
		}
		if victim == nil || pl.readyAt.Before(victim.readyAt) {
			victim = pl
		}
	}
	if victim == nil {
		return false
	}
	p.finishPreloadLocked(victim, PreloadEvicted)
	return true
}

// shrinkPreloadsLocked evicts preloads until their reservations fit the
// preload share of the current budget. It gives up the cheapest first: ready
// preloads no reader is using, then running preloads, and last ready preloads
// whose file is being read. Must be called with p.mu held.
func (p *Pool) shrinkPreloadsLocked() {
	for p.reservedPreloadBytesLocked() > preloadProtectionCapacity(p.readaheadBudget) {
		if p.evictIdlePreloadLocked() {
			continue
		}
		var victim *preload
		for _, pl := range p.preloads {
			if !pl.reserved {
				continue
			}
			if victim == nil || (pl.state == PreloadRunning && victim.state != PreloadRunning) {
				victim = pl
			}
		}
		if victim == nil {
			return
		}
		p.finishPreloadLocked(victim, PreloadEvicted)
	}
}

// releaseReadPreloadsLocked releases the ready preloads whose file was read
// and has no reader left, including a lingering one: playback of the file has
// ended, so its cache has served its purpose. Must be called with p.mu held.
func (p *Pool) releaseReadPreloadsLocked() {
	released := false
	for infoHash, pl := range p.preloads {
		if pl.state == PreloadReady && pl.read && !p.fileHasReadersLocked(pl.file) {
			p.removePreloadLocked(infoHash)
			released = true
		}
	}
	if released {
		p.dispatchPreloadsLocked()
	}
}

// fileHasReadersLocked reports whether file has an active or lingering
// reader. Must be called with p.mu held.
func (p *Pool) fileHasReadersLocked(file *torrent.File) bool {
	for _, sr := range p.readers {
		if sr.file == file {
			return true
		}
	}
	return false
}

// preloadCoversFileLocked reports whether a held preload reservation protects
// both the given file's head and tail. Must be called with p.mu held.
func (p *Pool) preloadCoversFileLocked(infoHash metainfo.Hash, filePath string) bool {
	pl := p.preloads[infoHash]
	return pl != nil && pl.reserved && pl.reservation.coversBoundaries && pl.reservation.filePath == filePath
}

// finishPreloadLocked stops a preload that failed or was evicted. It keeps
// reporting that final state for the ready TTL. Must be called with p.mu held.
func (p *Pool) finishPreloadLocked(pl *preload, state PreloadState) {
	p.stopPreloadLocked(pl)
	pl.state = state
	pl.finishedAt = time.Now()
	p.logger.Debug("preload finished",
		slog.String("hash", pl.infoHash.HexString()),
		slog.String("file", pl.file.Path()),
		slog.String("state", state.String()))
}

// removePreloadLocked stops a torrent's preload and forgets it. Must be called
// with p.mu held.
func (p *Pool) removePreloadLocked(infoHash metainfo.Hash) {
	pl := p.preloads[infoHash]
	if pl == nil {
		return
	}
	delete(p.preloads, infoHash)
	p.stopPreloadLocked(pl)
}

// stopPreloadLocked stops a preload's watcher and releases its priority
// claims, memory reservation, and protection. A queued preload leaves the
// queue on the next dispatch. Must be called with p.mu held.
func (p *Pool) stopPreloadLocked(pl *preload) {
	if pl.cancel != nil {
		pl.cancel()
		pl.cancel = nil
	}
	p.unclaimPreloadLocked(pl)
	p.releasePreloadReservationLocked(pl)
}

// expirePreloads removes preloads whose torrent closed, ready preloads whose
// file has not been read within the ready TTL, read preloads whose file has no
// reader left, and failed or evicted preloads that have reported their final
// state for the ready TTL.
func (p *Pool) expirePreloads() {
	p.mu.Lock()
	defer p.mu.Unlock()

	ttl := p.cfg.PreloadReadyTTL
	now := time.Now()
	removed := false
	for infoHash, pl := range p.preloads {
		switch {
		case torrentClosed(pl.file):
			p.removePreloadLocked(infoHash)
			removed = true
		case pl.state == PreloadReady:
			if p.fileHasReadersLocked(pl.file) {
				continue
			}
			if pl.read || (ttl > 0 && now.Sub(pl.readyAt) >= ttl) {
				p.removePreloadLocked(infoHash)
				removed = true
			}
		case pl.state == PreloadFailed || pl.state == PreloadEvicted:
			if ttl > 0 && now.Sub(pl.finishedAt) >= ttl {
				delete(p.preloads, infoHash)
			}
		}
	}
	if removed {
		p.dispatchPreloadsLocked()
	}
}
