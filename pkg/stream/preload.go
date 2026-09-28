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
	"github.com/torrplay/torrplay/pkg/storage"
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
	// PreloadFailed could not reserve memory with nothing left to wait for,
	// or its torrent completed no data for the stall timeout.
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
	defaultPreloadReadyTTL     = 5 * time.Minute
	defaultPreloadStallTimeout = 2 * time.Minute

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
	minSharedPreloadBytes = 2 * int64(defaultFileBoundaryBytes)
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
	// fileRead reports that a reader of the preload's file was acquired since
	// the preload was requested. Once its file has no reader left, a read
	// preload has served its purpose and is released.
	fileRead bool
	// finishedAt is when the preload failed or was evicted.
	finishedAt time.Time
	// headEnd, tailStart, and tailEnd are the file-relative byte ranges
	// [0, headEnd) and [tailStart, tailEnd) the preload caches. An empty tail
	// range caches only the head.
	headEnd, tailStart, tailEnd int64
	infoHash                    metainfo.Hash
	mode                        StorageMode
	pieces                      []int
	// progressAt is when a running preload started running, last completed a
	// piece, or last saw its torrent complete more data. The stall timeout
	// runs from it.
	progressAt time.Time
	// readyAt is when the preload first became ready. The ready TTL of a
	// preload whose file has not been read runs from it.
	readyAt     time.Time
	reservation preloadReservation
	reserved    bool
	state       PreloadState
	targetBytes int64
	// torrentCompleted is the torrent's completed bytes when progressAt was
	// last set.
	torrentCompleted int64
}

// preloadReservation is the protection budget held by a memory-storage
// preload, the pieces it protects, and whether it protects both its file's
// head and tail.
type preloadReservation struct {
	bytes            int64
	coversBoundaries bool
	// headStart, headEnd, tailStart, and tailEnd are the inclusive piece
	// ranges protected from eviction while the reservation is held.
	headStart, headEnd, tailStart, tailEnd int
	// id identifies the reservation's protection in the registry. It is drawn
	// from the reader ID sequence, so it never collides with a reader's.
	id uint64
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
	to := file.Torrent()
	infoHash := to.InfoHash()

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
	pl.completedBytes, _ = pl.progress(completedPieces(to, pl.pieces))
	pl.fileRead = p.fileHasReadersLocked(file)
	to.AllowDataDownload()
	p.preloads[infoHash] = pl
	p.preloadQueue = append(p.preloadQueue, pl)
	p.wakeExpireLoop()
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
// and whether every piece of its ranges is complete, given the completion of
// each of the preload's pieces.
func (pl *preload) progress(complete map[int]bool) (completedBytes int64, allComplete bool) {
	pieceComplete := func(index int) bool { return complete[index] }
	pieceLength := pl.file.Torrent().Info().PieceLength
	completedBytes = completedRangeBytes(pieceComplete, pieceLength, pl.file.Offset(), 0, pl.headEnd) +
		completedRangeBytes(pieceComplete, pieceLength, pl.file.Offset(), pl.tailStart, pl.tailEnd)
	for _, index := range pl.pieces {
		if !complete[index] {
			return completedBytes, false
		}
	}
	return completedBytes, true
}

// completedPieces returns the completion of each of the given ascending
// pieces of to, read under a single acquisition of the torrent client lock.
func completedPieces(to *torrent.Torrent, pieces []int) map[int]bool {
	complete := make(map[int]bool, len(pieces))
	next := 0
	start := 0
	for _, run := range to.PieceStateRuns() {
		end := start + run.Length
		for ; next < len(pieces) && pieces[next] < end; next++ {
			complete[pieces[next]] = run.Complete
		}
		start = end
	}
	return complete
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

// markProgress records that the preload made progress at now, together with
// its torrent's completed bytes at that time.
func (pl *preload) markProgress(now time.Time) {
	pl.progressAt = now
	pl.torrentCompleted = pl.file.Torrent().BytesCompleted()
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

	startend := max(int64(defaultFileBoundaryBytes), info.PieceLength)
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
	headStart, headEndPiece, tailStartPiece, tailEndPiece, ok := filePieceRanges(file, headEnd, tailStart, tailEnd)
	if !ok {
		return nil, false
	}
	return &preload{
		file:        file,
		fileIndex:   slices.Index(file.Torrent().Files(), file),
		headEnd:     headEnd,
		infoHash:    file.Torrent().InfoHash(),
		mode:        mode,
		pieces:      slices.Collect(boundaryPieces(headStart, headEndPiece, tailStartPiece, tailEndPiece)),
		state:       PreloadQueued,
		tailEnd:     tailEnd,
		tailStart:   tailStart,
		targetBytes: headEnd + (tailEnd - tailStart),
		reservation: preloadReservation{
			bytes:            boundaryPieceBytes(info, headStart, headEndPiece, tailStartPiece, tailEndPiece),
			coversBoundaries: preloadCoversBoundaries(file.Length(), headEnd, tailStart, tailEnd),
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
		headStartPiece, headEndPiece, tailStartPiece, tailEndPiece, ok := filePieceRanges(file, headEnd, tailStart, tailEnd)
		if !ok || boundaryPieceBytes(info, headStartPiece, headEndPiece, tailStartPiece, tailEndPiece) <= budget {
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
	playbackReserve := min(totalBudget/2, 2*int64(defaultFileBoundaryBytes))
	return max(totalBudget-playbackReserve, 0)
}

// dispatchPreloadsLocked starts queued preloads in FIFO order while fewer than
// maxConcurrentPreloads are running. A memory-storage preload first reserves
// its pieces, evicting cached preloads that no reader is using if it must. When
// its pieces do not fit, it waits for a running preload to become ready and
// evictable, or fails when none is running. Must be called with p.mu held.
func (p *Pool) dispatchPreloadsLocked() {
	for len(p.preloadQueue) > 0 && p.runningPreloadCountLocked() < maxConcurrentPreloads {
		pl := p.preloadQueue[0]
		if p.preloads[pl.infoHash] != pl || pl.state != PreloadQueued {
			p.preloadQueue = p.preloadQueue[1:]
			continue
		}
		// A ready preload that lost a piece waits here still holding its
		// reservation.
		if pl.mode == MemoryStorage && !pl.reserved && !p.reservePreloadLocked(pl) {
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

// startPreloadLocked claims a preload's pieces and starts its watcher, unless
// the preload is already watched. Must be called with p.mu held.
func (p *Pool) startPreloadLocked(pl *preload) {
	pl.state = PreloadRunning
	pl.markProgress(time.Now())
	p.claimPreloadLocked(pl)
	if pl.cancel != nil {
		return
	}
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
	to := pl.file.Torrent()
	// Subscribe before reading the piece states, so no change between the
	// read and the wait is missed. Changes are applied in order, so the
	// tracked states end at the latest ones.
	sub := to.SubscribePieceStateChanges()
	defer sub.Close()
	pieces := completedPieces(to, pl.pieces)

	for {
		completedBytes, complete := pl.progress(pieces)
		p.mu.Lock()
		if ctx.Err() != nil {
			p.mu.Unlock()
			return
		}
		p.updatePreloadLocked(pl, completedBytes, complete)
		p.mu.Unlock()

		if !waitForPieceChange(ctx, to, sub.Values, pieces) {
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

// waitForPieceChange blocks until the state of a piece in complete changes,
// records the new completion in complete, and records the changes already
// delivered as well, so that a burst causes one check. It returns false when
// ctx ends or the torrent closes.
func waitForPieceChange(ctx context.Context, to *torrent.Torrent, changes <-chan torrent.PieceStateChange, complete map[int]bool) bool {
	record := func(change torrent.PieceStateChange) bool {
		if _, watched := complete[change.Index]; !watched {
			return false
		}
		complete[change.Index] = change.Complete
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return false
		case <-to.Closed():
			return false
		case change, ok := <-changes:
			if !ok {
				return false
			}
			if !record(change) {
				continue
			}
			for {
				select {
				case change, ok := <-changes:
					if !ok {
						return false
					}
					record(change)
				default:
					return true
				}
			}
		}
	}
}

// updatePreloadLocked records a preload's progress. A running preload whose
// pieces are all complete becomes ready and gives up its priority claims. A
// ready preload that lost a piece is removed when it is due for removal
// anyway; otherwise it runs again, or waits first in the queue, keeping its
// reservation, while every preload slot is taken. Must be called with p.mu
// held.
func (p *Pool) updatePreloadLocked(pl *preload, completedBytes int64, complete bool) {
	if p.closed || p.preloads[pl.infoHash] != pl {
		return
	}
	if completedBytes > pl.completedBytes {
		pl.markProgress(time.Now())
	}
	pl.completedBytes = completedBytes
	switch {
	case pl.state == PreloadRunning && complete:
		pl.state = PreloadReady
		now := time.Now()
		// A preload that downloads a lost piece again keeps the ready TTL it
		// started when it first became ready, as one whose piece returns
		// while it waits does.
		if pl.readyAt.IsZero() {
			pl.readyAt = now
		}
		p.unclaimPreloadLocked(pl)
		p.logger.Debug("preload ready",
			slog.String("hash", pl.infoHash.HexString()),
			slog.String("file", pl.file.Path()))
		// Playback that started and ended while the preload ran no longer
		// needs its cache.
		if p.cachedPreloadExpiredLocked(pl, now) {
			p.removePreloadLocked(pl.infoHash)
		}
		p.dispatchPreloadsLocked()
	case pl.state == PreloadReady && !complete:
		// A preload that is due for removal is not worth downloading again.
		if p.cachedPreloadExpiredLocked(pl, time.Now()) {
			p.removePreloadLocked(pl.infoHash)
			p.dispatchPreloadsLocked()
			return
		}
		// Downloading again takes a preload slot like any other, so while
		// every slot is taken the preload waits first in the queue, keeping
		// its reservation and watcher.
		if p.runningPreloadCountLocked() >= maxConcurrentPreloads {
			pl.state = PreloadQueued
			p.preloadQueue = slices.Insert(p.preloadQueue, 0, pl)
			p.logger.Debug("preload lost a piece and waits for a slot",
				slog.String("hash", pl.infoHash.HexString()),
				slog.String("file", pl.file.Path()))
			return
		}
		pl.state = PreloadRunning
		pl.markProgress(time.Now())
		p.claimPreloadLocked(pl)
		p.logger.Debug("preload lost a piece and runs again",
			slog.String("hash", pl.infoHash.HexString()),
			slog.String("file", pl.file.Path()))
	case pl.state == PreloadQueued && pl.cancel != nil && complete:
		// A ready preload waiting for a slot got its piece back meanwhile.
		pl.state = PreloadReady
	}
}

// claimPreloadLocked claims every piece of a preload at PiecePriorityHigh.
// Must be called with p.mu held.
func (p *Pool) claimPreloadLocked(pl *preload) {
	planned := make([]prioritizedPiece, 0, len(pl.pieces))
	for _, index := range pl.pieces {
		planned = append(planned, prioritizedPiece{index: index, priority: torrent.PiecePriorityHigh})
	}
	pl.claimed = p.claimLocked(pl, pl.file.Torrent(), pl.claimed, planned)
}

// unclaimPreloadLocked removes a preload's priority claims. Must be called
// with p.mu held.
func (p *Pool) unclaimPreloadLocked(pl *preload) {
	if len(pl.claimed) == 0 {
		return
	}
	p.unclaimLocked(pl, pl.file.Torrent(), pl.claimed)
	pl.claimed = nil
}

// reservePreloadLocked reserves a memory-storage preload's pieces and protects
// them from eviction, evicting cached preloads that no reader is using, oldest
// first, when the preload share is full. It returns false when the pieces do
// not fit even then. Must be called with p.mu held.
func (p *Pool) reservePreloadLocked(pl *preload) bool {
	for pl.reservation.bytes > preloadProtectionCapacity(p.readaheadBudget)-p.reservedPreloadBytesLocked() {
		if !p.evictIdlePreloadLocked() {
			return false
		}
	}
	p.nextID++
	pl.reservation.id = p.nextID
	pl.reserved = true
	if p.cfg.Registry != nil {
		p.cfg.Registry.SetProtection(pl.infoHash, pl.reservation.id, storage.Protection{Active: []storage.PieceRange{
			{Start: pl.reservation.headStart, End: pl.reservation.headEnd},
			{Start: pl.reservation.tailStart, End: pl.reservation.tailEnd},
		}})
	}
	p.refreshReadaheadLocked()
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
		p.cfg.Registry.ClearProtection(pl.infoHash, pl.reservation.id)
	}
	p.refreshReadaheadLocked()
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

// evictIdlePreloadLocked evicts the oldest preload holding a cache, ready or
// waiting for a slot after losing a piece, skipping preloads whose file is
// being read. It returns false when there is none. Must be called with p.mu
// held.
func (p *Pool) evictIdlePreloadLocked() bool {
	var victim *preload
	for _, pl := range p.preloads {
		if !pl.holdsCache() || !pl.reserved || p.fileHasReadersLocked(pl.file) {
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
// preload share of the current budget. It gives up the cheapest first: cached
// preloads no reader is using, then running preloads, and last cached
// preloads whose file is being read. Must be called with p.mu held.
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

// releaseExpiredPreloadsLocked releases the preloads holding a cache that is
// due for removal, such as one whose file was read and has no reader left,
// including a lingering one: playback of the file has ended, so its cache has
// served its purpose. Must be called with p.mu held.
func (p *Pool) releaseExpiredPreloadsLocked() {
	released := false
	now := time.Now()
	for infoHash, pl := range p.preloads {
		if p.cachedPreloadExpiredLocked(pl, now) {
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
// both the head and tail of file, of the torrent with infoHash. Must be called
// with p.mu held.
func (p *Pool) preloadCoversFileLocked(infoHash metainfo.Hash, file *torrent.File) bool {
	pl := p.preloads[infoHash]
	return pl != nil && pl.file == file && pl.reserved && pl.reservation.coversBoundaries
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

// holdsCache reports whether the preload holds its cache without downloading:
// it is ready, or waits for a slot with its reservation after losing a piece.
func (pl *preload) holdsCache() bool {
	return pl.state == PreloadReady || (pl.state == PreloadQueued && pl.reserved)
}

// cachedPreloadExpiredLocked reports whether a preload holding its cache,
// ready or queued with its reservation after losing a piece, is due for
// removal at now: its file has no reader, and it was read or has stayed unread
// for the ready TTL since it became ready. Must be called with p.mu held.
func (p *Pool) cachedPreloadExpiredLocked(pl *preload, now time.Time) bool {
	if !pl.holdsCache() || p.fileHasReadersLocked(pl.file) {
		return false
	}
	ttl := p.cfg.PreloadReadyTTL
	return pl.fileRead || (ttl > 0 && now.Sub(pl.readyAt) >= ttl)
}

// expirePreloads removes preloads whose torrent closed, preloads holding a
// cache whose file has not been read within the ready TTL, read preloads whose
// file has no reader left, and failed or evicted preloads that have reported their final
// state for the ready TTL. It fails running preloads whose torrent completed
// no data within the stall timeout. A preload whose own pieces wait behind
// other viewers' playback of the torrent is not stalled, because the engine is
// shared: it downloads with the bandwidth playback leaves.
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
		case p.cachedPreloadExpiredLocked(pl, now):
			p.removePreloadLocked(infoHash)
			removed = true
		case pl.state == PreloadRunning:
			stall := p.cfg.PreloadStallTimeout
			if stall <= 0 {
				continue
			}
			if pl.file.Torrent().BytesCompleted() > pl.torrentCompleted {
				pl.markProgress(now)
				continue
			}
			if now.Sub(pl.progressAt) >= stall {
				p.finishPreloadLocked(pl, PreloadFailed)
				p.logger.Warn("preload stalled",
					slog.String("hash", infoHash.HexString()),
					slog.String("file", pl.file.Path()),
					slog.Duration("stalled", now.Sub(pl.progressAt)))
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
