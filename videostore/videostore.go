// Package videostore contains the implementation of the video storage camera component.
package videostore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/viam-modules/video-store/videostore/indexer"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	rutils "go.viam.com/rdk/utils"
	"go.viam.com/rdk/utils/diskusage"
	"go.viam.com/utils"
)

// Model is the model for the video storage camera component.
var Model = resource.ModelNamespace("viam").WithFamily("video").WithModel("storage")

const (
	// Constant values for the video storage camera component.
	fallbackDeletionLimitMultiplier = 1.1
	fallbackDeleterIntervalMinutes  = 10 // minutes
	segmentSeconds                  = 30 // seconds
	defaultContainer                = "mp4"
	retryIntervalSeconds            = 1         // seconds
	asyncTimeoutSeconds             = 60        // seconds
	streamingChunkSize              = 64 * 1024 // bytes (64KB)
)

var (
	tempPath                    = os.TempDir()
	windowsTmpStoragePathPrefix = filepath.Join(os.TempDir(), "viam", "video-storage")
)

var presets = map[string]struct{}{
	"ultrafast": {},
	"superfast": {},
	"veryfast":  {},
	"faster":    {},
	"fast":      {},
	"medium":    {},
	"slow":      {},
	"slower":    {},
	"veryslow":  {},
}

var allowedContainers = map[string]struct{}{
	"mp4":  {},
	"fmp4": {},
}

type videostore struct {
	latestFrame *atomic.Value
	typ         SourceType
	config      Config
	logger      logging.Logger

	workers *utils.StoppableWorkers

	rawSegmenter *RawSegmenter
	concater     *concater

	renameWorker *utils.StoppableWorkers
	indexer      *indexer.Indexer
}

// VideoStore stores video and provides APIs to request the stored video.
type VideoStore interface {
	Fetch(ctx context.Context, r *FetchRequest) (*FetchResponse, error)
	Save(ctx context.Context, r *SaveRequest) (*SaveResponse, error)
	FetchStream(ctx context.Context, r *FetchRequest, emit func([]byte) error) error
	Close()
	GetStorageState(ctx context.Context) (*StorageState, error)
}

// CodecType repreasents a codec.
type CodecType int

const (
	// CodecTypeUnknown is an invalid type.
	CodecTypeUnknown CodecType = iota
	// CodecTypeH264 represents h264 codec.
	CodecTypeH264
	// CodecTypeH265 represents h265 codec.
	CodecTypeH265
)

func (t CodecType) String() string {
	switch t {
	case CodecTypeUnknown:
		return "CodecTypeUnknown"
	case CodecTypeH264:
		return "CodecTypeH264"
	case CodecTypeH265:
		return "CodecTypeH265"
	default:
		return "CodecTypeUnknown"
	}
}

// RTPVideoStore stores video derived from RTP packets and provides APIs to request the stored video.
type RTPVideoStore interface {
	VideoStore
	Segmenter() *RawSegmenter
}

// SaveRequest is the request to the Save method.
type SaveRequest struct {
	From      time.Time
	To        time.Time
	Metadata  string // String added to the saved filename (<vs_name>_<from>_<metadata>.mp4)
	Container string // Video container format (e.g. "mp4", "fmp4")
	Async     bool
}

// SaveResponse is the response to the Save method.
type SaveResponse struct {
	Filename string
}

// normalizeContainer returns a normalized container ("mp4" or "fmp4"),
// defaulting to defaultContainer when empty, or an error if unsupported.
func normalizeContainer(c string) (string, error) {
	if c == "" {
		return defaultContainer, nil
	}
	lc := strings.ToLower(c)
	if _, ok := allowedContainers[lc]; !ok {
		return "", fmt.Errorf("invalid container type: %s", c)
	}
	return lc, nil
}

// Validate returns an error if the SaveRequest is invalid.
func (r *SaveRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	if r.To.After(time.Now()) {
		return errors.New("'to' timestamp is in the future")
	}
	c, err := normalizeContainer(r.Container)
	if err != nil {
		return err
	}
	r.Container = c
	return nil
}

// FetchRequest is the request to the Fetch method.
type FetchRequest struct {
	From      time.Time
	To        time.Time
	Container string
}

// FetchResponse is the resonse to the Fetch method.
type FetchResponse struct {
	Video []byte
}

// Validate returns an error if the FetchRequest is invalid.
func (r *FetchRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	c, err := normalizeContainer(r.Container)
	if err != nil {
		return err
	}
	r.Container = c
	return nil
}

// StorageState summarizes the state of the stored video segments and storage config info.
type StorageState struct {
	VideoRanges              indexer.VideoRanges
	StorageLimitGB           int
	DeviceStorageRemainingGB float64
	StoragePath              string
}

// NewFramePollingVideoStore returns a VideoStore that stores video it encoded from polling frames from a camera.Camera.
func NewFramePollingVideoStore(ctx context.Context, config Config, logger logging.Logger) (VideoStore, error) {
	if config.Type != SourceTypeFrame {
		return nil, fmt.Errorf("config type must be %s", SourceTypeFrame)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	vs := &videostore{
		latestFrame:  &atomic.Value{},
		typ:          config.Type,
		logger:       logger,
		config:       config,
		workers:      utils.NewBackgroundStoppableWorkers(),
		renameWorker: utils.NewBackgroundStoppableWorkers(),
	}
	if err := vsutils.CreateDir(config.Storage.StoragePath); err != nil {
		return nil, err
	}
	if err := vsutils.CreateDir(vs.config.Storage.UploadPath); err != nil {
		return nil, err
	}
	var directStoragePath string
	var renamer *renamer
	if runtime.GOOS == "windows" {
		var err error
		directStoragePath, renamer, err = setupWindowsStoragePath(logger, config.Storage.StoragePath, config.Name)
		if err != nil {
			return nil, err
		}
	} else {
		directStoragePath = config.Storage.StoragePath
	}

	// Create concater to handle concatenation of video clips when requested.
	concater, err := newConcater(
		config.Storage.StoragePath,
		config.Storage.UploadPath,
		logger,
	)
	if err != nil {
		return nil, err
	}
	vs.concater = concater

	encoder, err := newEncoder(
		vs.config.Encoder,
		vs.config.FramePoller.Framerate,
		directStoragePath,
		logger,
	)
	if err != nil {
		return nil, err
	}

	if err := encoder.initialize(); err != nil {
		logger.Warnf("encoder init failed: %s", err.Error())
		return nil, err
	}

	if renamer != nil {
		vs.renameWorker.Add(renamer.processSegments)
	}

	vs.indexer = indexer.NewIndexer(config.Storage.StoragePath, config.Storage.SizeGB, logger)
	if err := vs.indexer.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start indexer: %w", err)
	}

	vs.workers.Add(func(ctx context.Context) {
		vs.fetchFrames(
			ctx,
			config.FramePoller)
	})
	vs.workers.Add(func(ctx context.Context) {
		vs.processFrames(
			ctx,
			config.FramePoller.Framerate,
			encoder)
	})
	vs.workers.Add(vs.fallbackDeleter)

	return vs, nil
}

// NewReadOnlyVideoStore returns a VideoStore that can return stored video but doesn't create new video segements.
func NewReadOnlyVideoStore(ctx context.Context, config Config, logger logging.Logger) (VideoStore, error) {
	if config.Type != SourceTypeReadOnly {
		return nil, fmt.Errorf("config type must be %s", SourceTypeReadOnly)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if err := vsutils.CreateDir(config.Storage.StoragePath); err != nil {
		return nil, err
	}
	if err := vsutils.CreateDir(config.Storage.UploadPath); err != nil {
		return nil, err
	}

	concater, err := newConcater(
		config.Storage.StoragePath,
		config.Storage.UploadPath,
		logger,
	)
	if err != nil {
		return nil, err
	}

	indexer := indexer.NewIndexer(config.Storage.StoragePath, config.Storage.SizeGB, logger)
	if err := indexer.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start indexer: %w", err)
	}

	vs := &videostore{
		typ:      config.Type,
		concater: concater,
		indexer:  indexer,
		logger:   logger,
		config:   config,
		workers:  utils.NewBackgroundStoppableWorkers(),
	}

	return vs, nil
}

// NewRTPVideoStore returns a VideoStore that stores video it receives from the caller.
func NewRTPVideoStore(ctx context.Context, config Config, logger logging.Logger) (RTPVideoStore, error) {
	if config.Type != SourceTypeRTP {
		return nil, fmt.Errorf("config type must be %s", SourceTypeRTP)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if err := vsutils.CreateDir(config.Storage.StoragePath); err != nil {
		return nil, err
	}
	if err := vsutils.CreateDir(config.Storage.UploadPath); err != nil {
		return nil, err
	}

	var directStoragePath string
	var renamer *renamer
	if runtime.GOOS == "windows" {
		var err error
		directStoragePath, renamer, err = setupWindowsStoragePath(logger, config.Storage.StoragePath, config.Name)
		if err != nil {
			return nil, err
		}
	} else {
		directStoragePath = config.Storage.StoragePath
	}

	concater, err := newConcater(
		config.Storage.StoragePath,
		config.Storage.UploadPath,
		logger,
	)
	if err != nil {
		return nil, err
	}

	rawSegmenter, err := newRawSegmenter(
		directStoragePath,
		logger,
	)
	if err != nil {
		return nil, err
	}

	indexer := indexer.NewIndexer(config.Storage.StoragePath, config.Storage.SizeGB, logger)
	if err := indexer.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start indexer: %w", err)
	}

	vs := &videostore{
		typ:          config.Type,
		concater:     concater,
		rawSegmenter: rawSegmenter,
		indexer:      indexer,
		logger:       logger,
		config:       config,
		workers:      utils.NewBackgroundStoppableWorkers(),
		renameWorker: utils.NewBackgroundStoppableWorkers(),
	}
	vs.workers.Add(vs.fallbackDeleter)

	if renamer != nil {
		vs.renameWorker.Add(renamer.processSegments)
	}

	return vs, nil
}

func (vs *videostore) Segmenter() *RawSegmenter {
	return vs.rawSegmenter
}

func (vs *videostore) Fetch(_ context.Context, r *FetchRequest) (*FetchResponse, error) {
	// Convert incoming local times to UTC for consistent timestamp handling
	// All internal operations and segmenter timestamps are in UTC
	r.From = r.From.UTC()
	r.To = r.To.UTC()
	if err := r.Validate(); err != nil {
		return nil, err
	}
	vs.logger.Debug("fetch command received and validated")
	fetchFilePath := vsutils.GenerateOutputFilePath(
		vs.config.Storage.OutputFileNamePrefix,
		r.From,
		"",
		tempPath)

	// Always attempt to remove the concat file after the operation.
	// This handles error cases in Concat where it fails in the middle
	// of writing.
	defer func() {
		if _, statErr := os.Stat(fetchFilePath); os.IsNotExist(statErr) {
			vs.logger.Debugf("temporary file (%s) does not exist, skipping removal", fetchFilePath)
			return
		}
		if err := os.Remove(fetchFilePath); err != nil {
			vs.logger.Warnf("failed to delete temporary file (%s): %v", fetchFilePath, err)
		}
	}()
	if err := vs.concater.Concat(r.From, r.To, fetchFilePath, r.Container); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return nil, err
	}
	videoBytes, err := vsutils.ReadVideoFile(fetchFilePath)
	if err != nil {
		return nil, err
	}
	return &FetchResponse{Video: videoBytes}, nil
}

func (vs *videostore) FetchStream(ctx context.Context, r *FetchRequest, emit func([]byte) error) error {
	// Convert incoming local times to UTC for consistent timestamp handling
	// All internal operations and segmenter timestamps are in UTC
	r.From = r.From.UTC()
	r.To = r.To.UTC()
	if err := r.Validate(); err != nil {
		return err
	}
	vs.logger.Debug("fetch stream command received and validated")
	fetchFilePath := vsutils.GenerateOutputFilePath(
		vs.config.Storage.OutputFileNamePrefix,
		r.From,
		"",
		tempPath)

	// Always attempt to remove the concat file after the operation.
	// This handles error cases in Concat where it fails in the middle
	// of writing.
	defer func() {
		if _, statErr := os.Stat(fetchFilePath); os.IsNotExist(statErr) {
			vs.logger.Debugf("temporary file (%s) does not exist, skipping removal", fetchFilePath)
			return
		}
		if err := os.Remove(fetchFilePath); err != nil {
			vs.logger.Warnf("failed to delete temporary file (%s): %v", fetchFilePath, err)
		}
	}()
	if err := vs.concater.Concat(r.From, r.To, fetchFilePath, r.Container); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return err
	}

	// #nosec G304: path is internally generated, not user supplied
	file, err := os.Open(fetchFilePath)
	if err != nil {
		vs.logger.Error("failed to open file for streaming: ", err)
		return err
	}
	defer file.Close()

	// Read video file in 64KB chunks from disk and emit each chunk.
	// This avoids loading the entire file into memory at once
	// which is important for large video files.
	buf := make([]byte, streamingChunkSize)
	for {
		select {
		case <-ctx.Done():
			vs.logger.Debugf("context done during streaming: %v", ctx.Err())
			return ctx.Err()
		default:
		}
		n, err := file.Read(buf)
		if err != nil && err != io.EOF {
			vs.logger.Error("failed to read file for streaming: ", err)
			return err
		}
		if n == 0 {
			break
		}
		if err := emit(buf[:n]); err != nil {
			vs.logger.Error("failed to emit chunk: ", err)
			return err
		}
	}

	return nil
}

func (vs *videostore) Save(_ context.Context, r *SaveRequest) (*SaveResponse, error) {
	// Convert incoming local times to UTC for consistent timestamp handling
	// All internal operations and segmenter timestamps are in UTC
	r.From = r.From.UTC()
	r.To = r.To.UTC()
	if err := r.Validate(); err != nil {
		return nil, err
	}
	vs.logger.Debug("save command received and validated")
	uploadFilePath := vsutils.GenerateOutputFilePath(
		vs.config.Storage.OutputFileNamePrefix,
		r.From,
		r.Metadata,
		vs.config.Storage.UploadPath,
	)
	uploadFileName := filepath.Base(uploadFilePath)
	if r.Async {
		vs.logger.Debug("running save command asynchronously")
		vs.workers.Add(func(ctx context.Context) {
			vs.asyncSave(ctx, r.From, r.To, uploadFilePath)
		})
		return &SaveResponse{Filename: uploadFileName}, nil
	}

	if err := vs.concater.Concat(r.From, r.To, uploadFilePath, r.Container); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return nil, err
	}
	return &SaveResponse{Filename: uploadFileName}, nil
}

func (vs *videostore) fetchFrames(ctx context.Context, framePoller FramePollerConfig,
) {
	frameInterval := time.Second / time.Duration(framePoller.Framerate)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	var (
		data     []byte
		metadata camera.ImageMetadata
		err      error
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, metadata, err = framePoller.Camera.Image(ctx, rutils.MimeTypeJPEG, nil)
			if err != nil {
				vs.logger.Warn("failed to get frame from camera: ", err)
				time.Sleep(retryIntervalSeconds * time.Second)
				continue
			}
			if actualMimeType, _ := rutils.CheckLazyMIMEType(metadata.MimeType); actualMimeType != rutils.MimeTypeJPEG {
				vs.logger.Warnf("expected image in mime type %s got %s: ", rutils.MimeTypeJPEG, actualMimeType)
				continue
			}
			vs.latestFrame.Store(data)
		}
	}
}

func (vs *videostore) processFrames(
	ctx context.Context,
	framerate int,
	encoder *encoder,
) {
	defer encoder.close()
	frameInterval := time.Second / time.Duration(framerate)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame, ok := vs.latestFrame.Load().([]byte)
			if ok && frame != nil {
				encoder.encode(frame)
			}
		}
	}
}

// fallbackDeleter is a go routine that cleans up old clips if storage is full.
// It runs on interval and deletes the oldest clip(s) until the storage size is below the configured max.
// The actual deletion logic should only execute when the indexer is failing to clean up on its own.
// This is a worst case scenario prevention mechanism to ensure the disk doesn't fill up and lock up the system
// if we end up in a state where the indexer isn't doing its job properly.
func (vs *videostore) fallbackDeleter(ctx context.Context) {
	ticker := time.NewTicker(fallbackDeleterIntervalMinutes * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := vs.cleanupStorage(ctx); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return
				}
				vs.logger.Error("[fallback deleter] failed to clean up storage: ", err)
				continue
			}
		}
	}
}

func (vs *videostore) cleanupStorage(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	// Apply multiplier to the configured max storage size to avoid clashing with the indexer
	// so it only runs when the indexer is in a bad state.
	fallbackStorageLimit := int64(float64(vs.config.Storage.SizeGB)*fallbackDeletionLimitMultiplier) * vsutils.Gigabyte
	currStorageSize, err := vsutils.GetDirectorySize(vs.config.Storage.StoragePath)
	if err != nil {
		return err
	}
	if currStorageSize < fallbackStorageLimit {
		vs.logger.Debugf("[fallback deleter] storage size (%d bytes) is below "+
			"fallback limit (%d bytes), no cleanup needed", currStorageSize, fallbackStorageLimit)
		return nil
	}

	vs.logger.Warnf(
		"[fallback deleter] storage usage is %.1fx the configured max storage size limit (%d bytes used > %d bytes limit), "+
			"indexer is likely in an unhealthy state. Deleting oldest files manually to avoid system lockup",
		fallbackDeletionLimitMultiplier,
		currStorageSize,
		fallbackStorageLimit,
	)

	files, err := vsutils.GetSortedFiles(vs.config.Storage.StoragePath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		if currStorageSize < fallbackStorageLimit {
			break
		}

		fileInfo, err := os.Stat(file.Name)
		if err != nil {
			vs.logger.Warnf("[fallback deleter] failed to stat file during fallback deletion %s: %v", file.Name, err)
			continue
		}
		fileSize := fileInfo.Size()

		vs.logger.Infof("[fallback deleter] deleting file during fallback deletion: %s", file.Name)
		err = os.Remove(file.Name)
		if err != nil {
			vs.logger.Warnf("[fallback deleter] failed to delete file during fallback deletion %s: %v", file.Name, err)
			continue
		}
		vs.logger.Infof("[fallback deleter] deleted file during fallback deletion: %s", file.Name)
		currStorageSize -= fileSize
	}
	return nil
}

// asyncSave command will run the concat operation in the background.
// It waits for the segment duration before running to ensure the last segment
// is written to storage before concatenation.
// TODO: (seanp) Optimize this to immediately run as soon as the current segment is completed.
func (vs *videostore) asyncSave(ctx context.Context, from, to time.Time, path string) {
	segmentDur := time.Duration(segmentSeconds) * time.Second
	totalTimeout := time.Duration(asyncTimeoutSeconds)*time.Second + segmentDur
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	timer := time.NewTimer(segmentDur)
	defer timer.Stop()
	select {
	case <-timer.C:
		vs.logger.Debugf("executing concat for %s", path)
		err := vs.concater.Concat(from, to, path, defaultContainer)
		if err != nil {
			vs.logger.Error("failed to concat files ", err)
		}
		return
	case <-ctx.Done():
		vs.logger.Error("asyncSave operation cancelled or timed out")
		return
	}
}

// Close closes the video storage camera component.
func (vs *videostore) Close() {
	if vs.workers != nil {
		vs.workers.Stop()
	}

	if vs.indexer != nil {
		if err := vs.indexer.Close(); err != nil {
			vs.logger.Errorw("error closing indexer", "error", err)
		}
	}

	if vs.rawSegmenter != nil {
		if err := vs.rawSegmenter.Close(); err != nil {
			vs.logger.Errorw("error closing raw segmenter", "error", err)
		}
	}
	if vs.renameWorker != nil {
		// Stop will cause renamer to flush out all remaining files
		vs.renameWorker.Stop()
	}
}

func setupWindowsStoragePath(logger logging.Logger, storagePath, name string) (string, *renamer, error) {
	windowsTmpStoragePath := filepath.Join(windowsTmpStoragePathPrefix, name)
	logger.Debug("creating temporary storage path for windows", windowsTmpStoragePath)
	if err := vsutils.CreateDir(windowsTmpStoragePath); err != nil {
		return "", nil, fmt.Errorf("failed to create temporary storage path: %w", err)
	}
	renamer := newRenamer(windowsTmpStoragePath, storagePath, logger)
	return windowsTmpStoragePath, renamer, nil
}

// GetStorageState returns the current storage state for this VideoStore using the indexer.
func (vs *videostore) GetStorageState(ctx context.Context) (*StorageState, error) {
	if vs.indexer == nil {
		return nil, errors.New("indexer not initialized")
	}

	videoRangesResult, err := vs.indexer.GetVideoList(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get storage state from indexer: %w", err)
	}

	fsUsage, err := diskusage.Statfs(vs.config.Storage.StoragePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get filesystem stats for remaining space: %w", err)
	}

	return &StorageState{
		VideoRanges:              videoRangesResult,
		StorageLimitGB:           vs.config.Storage.SizeGB,
		StoragePath:              vs.config.Storage.StoragePath,
		DeviceStorageRemainingGB: float64(fsUsage.AvailableBytes) / float64(vsutils.Gigabyte),
	}, nil
}
