package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/ashishsinghbora/magicloder/internal/config"
	"github.com/ashishsinghbora/magicloder/internal/engine/chunk"
	"github.com/ashishsinghbora/magicloder/internal/engine/downloader"
	"github.com/ashishsinghbora/magicloder/internal/model"
	"github.com/ashishsinghbora/magicloder/internal/storage"
)

var (
	ErrDuplicateJob     = errors.New("a download job for this destination or URL is already active or queued")
	ErrJobNotFound      = errors.New("download job not found")
	ErrSchedulerStopped = errors.New("scheduler has been stopped")
)

// EventListener receives transfer state and progress update events.
type EventListener func(event string, dl *model.Download)

type activeJob struct {
	download  *model.Download
	cancel    context.CancelFunc
	done      chan struct{}
	paused    bool
	cancelled bool
}

// Scheduler coordinates download execution, concurrency caps, priorities, and lifecycles.
type Scheduler struct {
	cfg        *config.Config
	store      storage.Store
	prober     *downloader.Prober
	limiter    *Limiter
	listeners  []EventListener
	listenersMu sync.RWMutex

	mu         sync.Mutex
	activeJobs map[string]*activeJob
	queue      []*model.Download
	wakeCh     chan struct{}
	stopped    bool
	stopOnce   sync.Once
	ctx        context.Context
	cancel     context.CancelFunc
}

// New creates a new Scheduler instance.
func New(cfg *config.Config, store storage.Store, prober *downloader.Prober) *Scheduler {
	if prober == nil {
		prober = downloader.NewProber(nil)
	}

	var limiter *Limiter
	if cfg != nil && cfg.MaxGlobalBandwidth > 0 {
		limiter = NewLimiter(cfg.MaxGlobalBandwidth)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &Scheduler{
		cfg:        cfg,
		store:      store,
		prober:     prober,
		limiter:    limiter,
		activeJobs: make(map[string]*activeJob),
		queue:      make([]*model.Download, 0),
		wakeCh:     make(chan struct{}, 1),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// AddListener registers a callback for transfer lifecycle events.
func (s *Scheduler) AddListener(l EventListener) {
	s.listenersMu.Lock()
	defer s.listenersMu.Unlock()
	s.listeners = append(s.listeners, l)
}

func (s *Scheduler) emit(event string, dl *model.Download) {
	s.listenersMu.RLock()
	defer s.listenersMu.RUnlock()
	for _, l := range s.listeners {
		l(event, dl)
	}
}

// Start begins scheduler background queue processing and recovers unfinished jobs.
func (s *Scheduler) Start() error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return ErrSchedulerStopped
	}

	// Recover unfinished downloads from storage
	unfinished, err := s.store.GetUnfinishedDownloads(s.ctx)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to recover unfinished downloads: %w", err)
	}

	for _, dl := range unfinished {
		if dl.Status == model.StatusDownloading || dl.Status == model.StatusProbing {
			dl.Status = model.StatusQueued
			_ = s.store.UpdateDownloadStatus(s.ctx, dl.ID, model.StatusQueued, "")
		}
		if dl.Status == model.StatusQueued {
			s.queue = append(s.queue, dl)
		}
	}
	s.sortQueueLocked()
	s.mu.Unlock()

	go s.scheduleLoop()
	s.wake()
	return nil
}

// Stop shuts down the scheduler and cancels all active downloads cleanly.
func (s *Scheduler) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		s.stopped = true
		s.cancel()

		for _, j := range s.activeJobs {
			j.cancel()
		}
		s.mu.Unlock()
	})
}

// Submit queues a new download job.
func (s *Scheduler) Submit(dl *model.Download) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return ErrSchedulerStopped
	}

	// Check duplicates
	for _, j := range s.activeJobs {
		if j.download.Destination == dl.Destination || j.download.URL == dl.URL {
			return ErrDuplicateJob
		}
	}
	for _, q := range s.queue {
		if q.Destination == dl.Destination || q.URL == dl.URL {
			return ErrDuplicateJob
		}
	}

	dl.Status = model.StatusQueued
	dl.CreatedAt = time.Now().UTC()
	dl.UpdatedAt = dl.CreatedAt

	if err := s.store.CreateDownload(s.ctx, dl); err != nil {
		return fmt.Errorf("failed to persist download: %w", err)
	}

	s.queue = append(s.queue, dl)
	s.sortQueueLocked()
	s.wake()
	s.emit("job_queued", dl)

	return nil
}

// Pause pauses an active or queued download job.
func (s *Scheduler) Pause(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If in active jobs
	if j, ok := s.activeJobs[id]; ok {
		j.paused = true
		j.cancel() // Signals executing goroutine to halt and finalize pause
		return nil
	}

	// If in queue
	for i, q := range s.queue {
		if q.ID == id {
			_ = q.TransitionTo(model.StatusPaused)
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			_ = s.store.UpdateDownloadStatus(s.ctx, id, model.StatusPaused, "")
			s.emit("job_paused", q)
			return nil
		}
	}

	// Check storage
	dl, err := s.store.GetDownload(s.ctx, id)
	if err != nil {
		return ErrJobNotFound
	}
	_ = dl.TransitionTo(model.StatusPaused)
	_ = s.store.UpdateDownloadStatus(s.ctx, id, model.StatusPaused, "")
	s.emit("job_paused", dl)
	return nil
}

// Resume re-enqueues a paused job.
func (s *Scheduler) Resume(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return ErrSchedulerStopped
	}

	dl, err := s.store.GetDownload(s.ctx, id)
	if err != nil {
		return ErrJobNotFound
	}

	if dl.Status != model.StatusPaused && dl.Status != model.StatusFailed {
		return fmt.Errorf("cannot resume job with status %s", dl.Status)
	}

	_ = dl.TransitionTo(model.StatusQueued)
	_ = s.store.UpdateDownloadStatus(s.ctx, id, model.StatusQueued, "")

	s.queue = append(s.queue, dl)
	s.sortQueueLocked()
	s.wake()
	s.emit("job_resumed", dl)
	return nil
}

// Cancel cancels a job and releases its resources.
func (s *Scheduler) Cancel(id string, deleteFiles bool) error {
	s.mu.Lock()

	var target *model.Download
	if j, ok := s.activeJobs[id]; ok {
		target = j.download
		j.cancelled = true
		j.cancel()
	} else {
		for i, q := range s.queue {
			if q.ID == id {
				target = q
				s.queue = append(s.queue[:i], s.queue[i+1:]...)
				_ = target.TransitionTo(model.StatusCancelled)
				_ = s.store.UpdateDownloadStatus(s.ctx, id, model.StatusCancelled, "Cancelled by user")
				s.emit("job_cancelled", target)
				break
			}
		}
	}

	if target == nil {
		var err error
		target, err = s.store.GetDownload(s.ctx, id)
		if err != nil {
			s.mu.Unlock()
			return ErrJobNotFound
		}
	}

	_ = target.TransitionTo(model.StatusCancelled)
	_ = s.store.UpdateDownloadStatus(s.ctx, id, model.StatusCancelled, "Cancelled by user")
	s.mu.Unlock()

	if deleteFiles {
		_ = target.TemporaryPath
		// Cleanup files if requested
	}

	s.emit("job_cancelled", target)
	s.wake()
	return nil
}

// Retry retries a failed or cancelled job.
func (s *Scheduler) Retry(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	dl, err := s.store.GetDownload(s.ctx, id)
	if err != nil {
		return ErrJobNotFound
	}

	if dl.Status != model.StatusFailed && dl.Status != model.StatusCancelled {
		return fmt.Errorf("cannot retry job in state %s", dl.Status)
	}

	_ = dl.TransitionTo(model.StatusQueued)
	dl.ErrorMsg = ""
	_ = s.store.UpdateDownload(s.ctx, dl)

	s.queue = append(s.queue, dl)
	s.sortQueueLocked()
	s.wake()
	s.emit("job_retried", dl)
	return nil
}

// Remove deletes a job from storage and active maps.
func (s *Scheduler) Remove(id string) error {
	_ = s.Cancel(id, true)
	return s.store.DeleteDownload(s.ctx, id)
}

func (s *Scheduler) wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

func (s *Scheduler) sortQueueLocked() {
	sort.SliceStable(s.queue, func(i, j int) bool {
		if s.queue[i].Priority != s.queue[j].Priority {
			return s.queue[i].Priority > s.queue[j].Priority // Higher priority first
		}
		return s.queue[i].CreatedAt.Before(s.queue[j].CreatedAt) // Earlier first
	})
}

func (s *Scheduler) scheduleLoop() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		case <-s.wakeCh:
		}

		s.mu.Lock()
		if s.stopped {
			s.mu.Unlock()
			return
		}

		maxConcurrent := 3
		if s.cfg != nil && s.cfg.MaxConcurrentDownloads > 0 {
			maxConcurrent = s.cfg.MaxConcurrentDownloads
		}

		for len(s.activeJobs) < maxConcurrent && len(s.queue) > 0 {
			nextJob := s.queue[0]
			s.queue = s.queue[1:]

			jobCtx, jobCancel := context.WithCancel(s.ctx)
			j := &activeJob{
				download: nextJob,
				cancel:   jobCancel,
				done:     make(chan struct{}),
			}
			s.activeJobs[nextJob.ID] = j

			go s.executeJob(jobCtx, j)
		}
		s.mu.Unlock()
	}
}

func (s *Scheduler) executeJob(ctx context.Context, j *activeJob) {
	dl := j.download
	defer func() {
		close(j.done)
		s.mu.Lock()
		delete(s.activeJobs, dl.ID)
		s.mu.Unlock()
		s.wake()
	}()

	// 1. Probing phase
	_ = dl.TransitionTo(model.StatusProbing)
	_ = s.store.UpdateDownloadStatus(ctx, dl.ID, model.StatusProbing, "")
	s.emit("job_probing", dl)

	probeRes, err := s.prober.Probe(ctx, dl.URL)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		_ = dl.TransitionTo(model.StatusFailed)
		_ = s.store.UpdateDownloadStatus(s.ctx, dl.ID, model.StatusFailed, err.Error())
		s.emit("job_failed", dl)
		return
	}

	dl.TotalSize = probeRes.ContentLength
	dl.SupportsRange = probeRes.SupportsRange
	dl.ETag = probeRes.ETag
	dl.LastModified = probeRes.LastModified
	dl.ContentType = probeRes.ContentType

	// 2. Reconciliation phase
	rec, err := downloader.ReconcileDownload(ctx, dl, probeRes, s.store)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		_ = dl.TransitionTo(model.StatusFailed)
		_ = s.store.UpdateDownloadStatus(s.ctx, dl.ID, model.StatusFailed, err.Error())
		s.emit("job_failed", dl)
		return
	}

	// 3. Plan chunks if range supported and not planned
	if dl.SupportsRange && dl.TotalSize > 0 && len(dl.Chunks) == 0 {
		numWorkers := s.cfg.DefaultWorkersPerDownload
		if dl.MaxConnections > 0 {
			numWorkers = dl.MaxConnections
		}
		planned, pErr := chunk.PlanChunks(dl.TotalSize, numWorkers, s.cfg.MinChunkSize)
		if pErr == nil {
			for i := range planned {
				planned[i].DownloadID = dl.ID
			}
			dl.Chunks = planned
			_ = s.store.SaveChunks(ctx, planned)
		}
	}

	_ = s.store.UpdateDownload(ctx, dl)

	// 4. Downloading phase
	_ = dl.TransitionTo(model.StatusDownloading)
	_ = s.store.UpdateDownloadStatus(ctx, dl.ID, model.StatusDownloading, "")
	s.emit("job_downloading", dl)

	// Debounced progress updater
	var lastPersist time.Time
	var progressMu sync.Mutex
	onProgress := func(completed, total int64) {
		progressMu.Lock()
		defer progressMu.Unlock()
		dl.CompletedBytes = completed
		now := time.Now()
		if now.Sub(lastPersist) >= 500*time.Millisecond {
			lastPersist = now
			_ = s.store.UpdateDownloadProgress(context.Background(), dl.ID, completed)
			s.emit("job_progress", dl)
		}
	}

	var downloadErr error
	if dl.SupportsRange && len(dl.Chunks) > 0 && !errors.Is(err, downloader.ErrFallbackToSingleStream) {
		opts := downloader.DefaultRangedOptions()
		opts.MaxWorkers = dl.MaxConnections
		opts.OnTotalProgress = onProgress
		opts.OnChunkProgress = func(chunkIndex int, deltaBytes, chunkCompleted int64) {
			// Chunk progress tracked in memory
		}

		downloadErr = downloader.DownloadRanged(ctx, dl.URL, dl.TemporaryPath, dl.Destination, dl.TotalSize, dl.Chunks, opts)
		if errors.Is(downloadErr, downloader.ErrFallbackToSingleStream) {
			// Fallback to single stream
			dl.SupportsRange = false
			s.emit("job_fallback_single_stream", dl)
			streamOpts := downloader.DefaultSingleStreamOptions()
			streamOpts.OnProgress = onProgress
			downloadErr = downloader.DownloadSingleStream(ctx, dl.URL, dl.TemporaryPath, dl.Destination, dl.TotalSize, streamOpts)
		}
	} else {
		streamOpts := downloader.DefaultSingleStreamOptions()
		streamOpts.OnProgress = onProgress
		downloadErr = downloader.DownloadSingleStream(ctx, dl.URL, dl.TemporaryPath, dl.Destination, dl.TotalSize, streamOpts)
	}

	if ctx.Err() != nil {
		s.mu.Lock()
		isPaused := j.paused
		isCancelled := j.cancelled
		s.mu.Unlock()

		if isPaused {
			_ = dl.TransitionTo(model.StatusPaused)
			_ = s.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusPaused, "")
			s.emit("job_paused", dl)
		} else if isCancelled {
			_ = dl.TransitionTo(model.StatusCancelled)
			_ = s.store.UpdateDownloadStatus(context.Background(), dl.ID, model.StatusCancelled, "Cancelled by user")
			s.emit("job_cancelled", dl)
		}
		return
	}

	if downloadErr != nil {
		_ = dl.TransitionTo(model.StatusFailed)
		dl.ErrorMsg = downloadErr.Error()
		_ = s.store.UpdateDownloadStatus(s.ctx, dl.ID, model.StatusFailed, dl.ErrorMsg)
		s.emit("job_failed", dl)
		return
	}

	// Success
	_ = dl.TransitionTo(model.StatusCompleted)
	dl.CompletedBytes = dl.TotalSize
	_ = s.store.UpdateDownloadStatus(s.ctx, dl.ID, model.StatusCompleted, "")
	_ = s.store.UpdateDownloadProgress(s.ctx, dl.ID, dl.CompletedBytes)
	s.emit("job_completed", dl)
	_ = rec
}
