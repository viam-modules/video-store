// Package videostore contains the implementation of the video storage camera component.
package videostore

/*
#include <libavutil/frame.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	rutils "go.viam.com/rdk/utils"
	"go.viam.com/utils"
)

// Model is the model for the video storage camera component.
var Model = resource.ModelNamespace("viam").WithFamily("video").WithModel("storage")

const (
	// Default values for the video storage camera component.
	defaultFramerate      = 20 // frames per second
	defaultSegmentSeconds = 30 // seconds
	defaultVideoBitrate   = 1000000
	defaultVideoPreset    = "medium"
	defaultVideoFormat    = "mp4"
	defaultUploadPath     = ".viam/capture/video-upload"
	defaultStoragePath    = ".viam/video-storage"
	defaultLogLevel       = "error"

	deleterInterval       = 1  // minutes
	retryInterval         = 1  // seconds
	asyncTimeout          = 60 // seconds
	numFetchFrameAttempts = 3  // iterations
	tempPath              = "/tmp"

	mimeTypeYUYV = "image/yuyv422"
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

type videostore struct {
	typ    SourceType
	config Config
	logger logging.Logger

	workers *utils.StoppableWorkers

	rawSegmenter *RawSegmenter
	concater     *concater
}

// VideoStore stores video and provides APIs to request the stored video
type VideoStore interface {
	Fetch(ctx context.Context, r *FetchRequest) (*FetchResponse, error)
	Save(ctx context.Context, r *SaveRequest) (*SaveResponse, error)
	Close()
}

// CodecType repreasents a codec
type CodecType int

const (
	// CodecTypeUnknown is an invalid type.
	CodecTypeUnknown CodecType = iota
	// CodecTypeH264 represents h264 codec
	CodecTypeH264
	// CodecTypeH265 represents h265 codec
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

// RTPVideoStore stores video derived from RTP packets and provides APIs to request the stored video
type RTPVideoStore interface {
	VideoStore
	Segmenter() *RawSegmenter
}

// SaveRequest is the request to the Save method
type SaveRequest struct {
	From     time.Time
	To       time.Time
	Metadata string
	Async    bool
}

// SaveResponse is the response to the Save method
type SaveResponse struct {
	Filename string
}

// Validate returns an error if the SaveRequest is invalid
func (r *SaveRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	if r.To.After(time.Now()) {
		return errors.New("'to' timestamp is in the future")
	}
	return nil
}

// FetchRequest is the request to the Fetch method
type FetchRequest struct {
	From time.Time
	To   time.Time
}

// FetchResponse is the resonse to the Fetch method
type FetchResponse struct {
	Video []byte
}

// Validate returns an error if the FetchRequest is invalid
func (r *FetchRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	return nil
}

// NewFramePollingVideoStore returns a VideoStore that stores video it encoded from polling frames from a camera.Camera
func NewFramePollingVideoStore(config Config, logger logging.Logger) (VideoStore, error) {
	if config.Type != SourceTypeFrame {
		return nil, fmt.Errorf("config type must be %s", SourceTypeFrame)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	vs := &videostore{
		typ:     config.Type,
		logger:  logger,
		config:  config,
		workers: utils.NewBackgroundStoppableWorkers(),
	}
	// Create concater to handle concatenation of video clips when requested.
	err := createDir(vs.config.Storage.UploadPath)
	if err != nil {
		return nil, err
	}
	vs.concater, err = newConcater(
		logger,
		config.Storage.StoragePath,
		config.Storage.UploadPath,
		defaultSegmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	encoder, err := newEncoder(
		vs.config.Encoder,
		vs.config.FramePoller.Framerate,
		vs.config.Storage.SizeGB,
		vs.config.Storage.StoragePath,
		logger,
	)
	if err != nil {
		return nil, err
	}
	vs.workers.Add(func(ctx context.Context) {
		fetchAndProcessFrames(
			ctx,
			config.FramePoller,
			encoder,
			vs.logger)
	})
	vs.workers.Add(vs.deleter)

	return vs, nil
}

// NewReadOnlyVideoStore returns a VideoStore that can return stored video but doesn't create new video segements
func NewReadOnlyVideoStore(config Config, logger logging.Logger) (VideoStore, error) {
	if config.Type != SourceTypeReadOnly {
		return nil, fmt.Errorf("config type must be %s", SourceTypeReadOnly)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if err := createDir(config.Storage.UploadPath); err != nil {
		return nil, err
	}

	concater, err := newConcater(
		logger,
		config.Storage.StoragePath,
		config.Storage.UploadPath,
		defaultSegmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	return &videostore{
		typ:      config.Type,
		concater: concater,
		logger:   logger,
		config:   config,
		workers:  utils.NewBackgroundStoppableWorkers(),
	}, nil
}

// NewRTPVideoStore returns a VideoStore that stores video it receives from the caller
func NewRTPVideoStore(config Config, logger logging.Logger) (RTPVideoStore, error) {
	if config.Type != SourceTypeRTP {
		return nil, fmt.Errorf("config type must be %s", SourceTypeRTP)
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if err := createDir(config.Storage.UploadPath); err != nil {
		return nil, err
	}

	concater, err := newConcater(
		logger,
		config.Storage.StoragePath,
		config.Storage.UploadPath,
		defaultSegmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	rawSegmenter, err := newRawSegmenter(logger,
		config.Storage.StoragePath,
		defaultSegmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	vs := &videostore{
		typ:          config.Type,
		concater:     concater,
		rawSegmenter: rawSegmenter,
		logger:       logger,
		config:       config,
		workers:      utils.NewBackgroundStoppableWorkers(),
	}

	vs.workers.Add(vs.deleter)
	return vs, nil
}

func (vs *videostore) Segmenter() *RawSegmenter {
	return vs.rawSegmenter
}

func (vs *videostore) Fetch(_ context.Context, r *FetchRequest) (*FetchResponse, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	vs.logger.Debug("fetch command received")
	fetchFilePath := generateOutputFilePath(
		vs.config.Storage.OutputFileNamePrefix,
		formatDateTimeToString(r.From),
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
	if err := vs.concater.Concat(r.From, r.To, fetchFilePath); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return nil, err
	}
	videoBytes, err := readVideoFile(fetchFilePath)
	if err != nil {
		return nil, err
	}
	return &FetchResponse{Video: videoBytes}, nil
}

func (vs *videostore) Save(_ context.Context, r *SaveRequest) (*SaveResponse, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	vs.logger.Debug("save command received")
	if err := r.Validate(); err != nil {
		return nil, err
	}
	uploadFilePath := generateOutputFilePath(
		vs.config.Storage.OutputFileNamePrefix,
		formatDateTimeToString(r.From),
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

	if err := vs.concater.Concat(r.From, r.To, uploadFilePath); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return nil, err
	}
	return &SaveResponse{Filename: uploadFileName}, nil
}

func fetchAndProcessFrames(
	ctx context.Context,
	framePoller FramePollerConfig,
	encoder *encoder,
	logger logging.Logger,
) {
	defer encoder.close()
	frameInterval := time.Second / time.Duration(framePoller.Framerate)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	var (
		data        []byte
		metadata    camera.ImageMetadata
		err         error
		initialized bool
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			data, metadata, err = framePoller.Camera.Image(ctx, rutils.MimeTypeJPEG, nil)
			if err != nil {
				logger.Warn("failed to get frame from camera: ", err)
				time.Sleep(retryInterval * time.Second)
				continue
			}
			if actualMimeType, _ := rutils.CheckLazyMIMEType(metadata.MimeType); actualMimeType != rutils.MimeTypeJPEG {
				logger.Warnf("expected image in mime type %s got %s: ", rutils.MimeTypeJPEG, actualMimeType)
				continue
			}
			if !initialized {
				if err := encoder.initialize(); err != nil {
					logger.Warnf("encoder init failed: %s", err.Error())
					continue
				}
				initialized = true
			}
			encoder.encode(data, time.Now())
		}
	}
}

// deleter is a go routine that cleans up old clips if storage is full. Runs on interval
// and deletes the oldest clip until the storage size is below the configured max.
func (vs *videostore) deleter(ctx context.Context) {
	ticker := time.NewTicker(deleterInterval * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Perform the deletion of the oldest clip
			if err := cleanupStorage(vs.config.Storage.StoragePath, vs.config.Storage.SizeGB, vs.logger); err != nil {
				vs.logger.Error("failed to clean up storage", err)
				continue
			}
		}
	}
}

func cleanupStorage(storagePath string, maxStorageSizeGB int, logger logging.Logger) error {
	maxStorageSize := int64(maxStorageSizeGB) * gigabyte
	currStorageSize, err := getDirectorySize(storagePath)
	if err != nil {
		return err
	}
	if currStorageSize < maxStorageSize {
		return nil
	}
	files, err := getSortedFiles(storagePath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if currStorageSize < maxStorageSize {
			break
		}
		logger.Debugf("deleting file: %s", file)
		err := os.Remove(file.name)
		if err != nil {
			return err
		}
		logger.Debugf("deleted file: %s", file)
		// NOTE: This is going to be super slow
		// we should speed this up
		currStorageSize, err = getDirectorySize(storagePath)
		if err != nil {
			return err
		}
	}
	return nil
}

// asyncSave command will run the concat operation in the background.
// It waits for the segment duration before running to ensure the last segment
// is written to storage before concatenation.
// TODO: (seanp) Optimize this to immediately run as soon as the current segment is completed.
func (vs *videostore) asyncSave(ctx context.Context, from, to time.Time, path string) {
	segmentDur := time.Duration(defaultSegmentSeconds) * time.Second
	totalTimeout := time.Duration(asyncTimeout)*time.Second + segmentDur
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	timer := time.NewTimer(segmentDur)
	defer timer.Stop()
	select {
	case <-timer.C:
		vs.logger.Debugf("executing concat for %s", path)
		err := vs.concater.Concat(from, to, path)
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
	if vs.rawSegmenter != nil {
		vs.rawSegmenter.Close()
	}
}
