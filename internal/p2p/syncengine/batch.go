package syncengine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/opensave/opensave/internal/delta"
)

// Pulling many small files, many to a request.
//
// A pull used to cost one HTTP round trip per file, one after the other.
// Measured on a Windows LAN, that came to ~200ms a file, of which writing the
// file (temp, fsync, rename) was ~2ms — so a save made of a quarter-million
// small files took half a day to move 50MB. Batching removes the round trips;
// the writing is the same as before, file by file, and just as safe: each
// file still goes through a PatchWriter (temp file, content verified against
// the manifest's hash, fsync, rename) and gets the peer's mtime.

const (
	// batchFileMaxBytes is the most a file may need fetched to go into a
	// batch. Anything bigger keeps the per-file path, which already fetches
	// its blocks in parallel and has nothing to gain here.
	batchFileMaxBytes = 256 << 10
	// batchMaxFiles and batchMaxBytes bound one request, well inside what the
	// responder accepts (p2p.fileBatchMaxFiles / fileBatchMaxBytes).
	batchMaxFiles = 256
	batchMaxBytes = 4 << 20
)

// batchWorkers is how many batch requests are in flight at once: enough to
// keep the link and the responder's disk busy while this side writes. A var
// only so a test can make the order deterministic.
var batchWorkers = 4

type batchJob struct {
	relPath   string
	localPath string
	remote    delta.FileEntry
	indices   []int
}

// changedBytes is how much of a file a pull has to fetch.
func changedBytes(f delta.FileEntry, indices []int) int64 {
	var n int64
	for _, idx := range indices {
		if idx >= 0 && idx < len(f.Blocks) {
			n += int64(f.Blocks[idx].Length)
		}
	}
	return n
}

// groupBatches splits jobs into requests of at most batchMaxFiles files and
// batchMaxBytes of blocks.
func groupBatches(jobs []batchJob) [][]batchJob {
	var out [][]batchJob
	var cur []batchJob
	var curBytes int64
	for _, j := range jobs {
		b := changedBytes(j.remote, j.indices)
		if len(cur) > 0 && (len(cur) >= batchMaxFiles || curBytes+b > batchMaxBytes) {
			out = append(out, cur)
			cur, curBytes = nil, 0
		}
		cur = append(cur, j)
		curBytes += b
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// pullBatched fetches and writes jobs, many files per request. It returns the
// paths it wrote, including on error, since those files are on disk.
func (e *Engine) pullBatched(ctx context.Context, batcher BatchFetcher, peer Peer, gameID, root string,
	jobs []batchJob, throttle *throttler, tracker *progressTracker, onProgress func(force bool)) ([]string, error) {

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex
		written  []string
		firstErr error
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}

	batches := groupBatches(jobs)
	workers := batchWorkers
	if workers > len(batches) {
		workers = len(batches)
	}
	work := make(chan []batchJob)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for batch := range work {
				done, err := e.pullOneBatch(ctx, batcher, peer, gameID, root, batch, throttle, tracker, onProgress)
				mu.Lock()
				written = append(written, done...)
				mu.Unlock()
				if err != nil {
					fail(err)
					return
				}
				// One line per batch instead of one per file: a quarter-million
				// "file updated" lines rotated every other message out of the log.
				if len(done) > 0 {
					e.Log("info", fmt.Sprintf("%d files updated (%s … %s)", len(done), done[0], done[len(done)-1]))
				}
				onProgress(true)
			}
		}()
	}
	for _, b := range batches {
		select {
		case work <- b:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if firstErr != nil {
		return written, firstErr
	}
	return written, ctx.Err()
}

// pullOneBatch fetches one batch and writes its files.
func (e *Engine) pullOneBatch(ctx context.Context, batcher BatchFetcher, peer Peer, gameID, root string,
	batch []batchJob, throttle *throttler, tracker *progressTracker, onProgress func(force bool)) ([]string, error) {

	req := make([]FileBlocksRequest, len(batch))
	for i, j := range batch {
		req[i] = FileBlocksRequest{RelPath: j.relPath, BlockIndices: j.indices, BlockSize: j.remote.BlockSize}
	}

	var resp []FileBlocks
	var err error
	const maxAttempts = 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err = batcher.FetchFileBatch(ctx, peer, gameID, root, req)
		if err == nil || errors.Is(err, ErrBatchUnsupported) {
			break
		}
		e.Log("warn", fmt.Sprintf("batch fetch attempt %d/%d of %d files failed: %v", attempt, maxAttempts, len(batch), err))
		if attempt < maxAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
	}
	if errors.Is(err, ErrBatchUnsupported) {
		return e.pullBatchOneByOne(ctx, peer, gameID, root, batch, throttle, tracker, onProgress)
	}
	if err != nil {
		return nil, fmt.Errorf("fetch batch of %d files: %w", len(batch), err)
	}

	byPath := make(map[string]FileBlocks, len(resp))
	for _, f := range resp {
		byPath[f.RelPath] = f
	}

	var written []string
	for _, j := range batch {
		if ctx.Err() != nil {
			return written, ctx.Err()
		}
		f, ok := byPath[j.relPath]
		if !ok {
			return written, fmt.Errorf("fetch blocks for %s: %s did not send it", j.relPath, peer.Name)
		}
		if f.Error != "" {
			return written, fmt.Errorf("fetch blocks for %s: %s", j.relPath, f.Error)
		}
		if err := writeBatchedFile(j, f.Blocks); err != nil {
			return written, err
		}
		written = append(written, j.relPath)
		n := changedBytes(j.remote, j.indices)
		tracker.add(n)
		onProgress(false)
		throttle.wait(ctx, n)
	}
	return written, nil
}

// writeBatchedFile writes one file from its fetched blocks, exactly as
// pullFile does: unchanged blocks from the copy on disk, the rest from the
// peer, verified and renamed into place by the PatchWriter.
func writeBatchedFile(j batchJob, blocks []BlockData) error {
	writer, err := delta.NewPatchWriter(j.localPath, j.remote)
	if err != nil {
		return fmt.Errorf("patch %s: %w", j.relPath, err)
	}
	committed := false
	defer func() {
		if !committed {
			writer.Abort()
		}
	}()
	incoming := make(map[int]bool, len(j.indices))
	for _, idx := range j.indices {
		incoming[idx] = true
	}
	if err := writer.SeedUnchanged(j.localPath, incoming); err != nil {
		return fmt.Errorf("patch %s: %w", j.relPath, err)
	}
	for _, b := range blocks {
		if err := writer.WriteBlock(b.Index, b.Data); err != nil {
			return fmt.Errorf("patch %s: %w", j.relPath, err)
		}
	}
	if err := writer.Commit(); err != nil {
		return fmt.Errorf("patch %s: %w", j.relPath, err)
	}
	committed = true
	if j.remote.MtimeMs > 0 {
		mtime := time.UnixMilli(int64(j.remote.MtimeMs))
		_ = os.Chtimes(j.localPath, mtime, mtime)
	}
	return nil
}

// pullBatchOneByOne is the fallback when the transport cannot batch for this
// peer after all: the same files, through the per-file path.
func (e *Engine) pullBatchOneByOne(ctx context.Context, peer Peer, gameID, root string,
	batch []batchJob, throttle *throttler, tracker *progressTracker, onProgress func(force bool)) ([]string, error) {

	var written []string
	for _, j := range batch {
		if err := e.pullFile(ctx, peer, FileRef{GameID: gameID, Root: root, RelPath: j.relPath}, j.localPath,
			j.remote, j.indices, throttle, tracker, onProgress); err != nil {
			return written, err
		}
		if j.remote.MtimeMs > 0 {
			mtime := time.UnixMilli(int64(j.remote.MtimeMs))
			_ = os.Chtimes(j.localPath, mtime, mtime)
		}
		written = append(written, j.relPath)
	}
	return written, nil
}
