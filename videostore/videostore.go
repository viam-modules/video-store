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
	"path/filepath"
	"sync/atomic"
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

	latestFrame atomic.Pointer[C.AVFrame]
	workers     *utils.StoppableWorkers

	rawSegmenter *rawSegmenter
	segmenter    *segmenter
	concater     Concater
}

// VideoStore stores video and provides APIs to request the stored video
type VideoStore interface {
	Fetch(ctx context.Context, r *FetchRequest) (*FetchResponse, error)
	Save(ctx context.Context, r *SaveRequest) (*SaveResponse, error)
	Close(ctx context.Context) error
}

// Concater concatinates video files within the time range
type Concater interface {
	Concat(from, to time.Time, path string) error
}

// H264RTPVideoStore stores h264 video derived from RTP packets and provides APIs to request the stored video
type H264RTPVideoStore interface {
	VideoStore
	InitH264(sps, pps []byte) error
	WritePacket(payload []byte, pts int64, isIDR bool) error
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
func NewFramePollingVideoStore(_ context.Context, config Config, logger logging.Logger) (VideoStore, error) {
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
		config.Storage.SegmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	// Only initialize mime handler, encoder, segmenter, and frame processing routines
	// if the source camera is available.
	cameraAvailable := config.FramePoller.Camera != nil
	if cameraAvailable {
		encoder, err := newEncoder(
			logger,
			vs.config.Encoder.Bitrate,
			vs.config.Encoder.Preset,
			vs.config.FramePoller.Framerate,
		)
		if err != nil {
			return nil, err
		}
		vs.segmenter, err = newSegmenter(
			logger,
			vs.config.Storage.SizeGB,
			vs.config.Storage.SegmentSeconds,
			vs.config.Storage.StoragePath,
			defaultVideoFormat,
		)
		if err != nil {
			return nil, err
		}
		// Start workers to process frames and clean up storage.
		vs.workers.Add(func(ctx context.Context) { vs.fetchFrames(ctx, config.FramePoller.Camera) })
		vs.workers.Add(func(ctx context.Context) { vs.processFrames(ctx, encoder) })
		vs.workers.Add(vs.deleter)
	}

	return vs, nil
}

// NewH264RTPVideoStore returns a VideoStore that stores video it receives from the caller
func NewH264RTPVideoStore(_ context.Context, config Config, logger logging.Logger) (H264RTPVideoStore, error) {
	if config.Type != SourceTypeH264RTPPacket {
		return nil, fmt.Errorf("config type must be %s", SourceTypeFrame)
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
		config.Storage.SegmentSeconds,
	)
	if err != nil {
		return nil, err
	}

	rawSegmenter, err := newRawSegmenter(logger,
		config.Storage.SizeGB,
		config.Storage.StoragePath,
		config.Storage.SegmentSeconds,
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
	vs.workers.Add(func(ctx context.Context) {
		ticker := time.NewTicker(deleterInterval * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Perform the deletion of the oldest clip
				if err := rawSegmenter.cleanupStorage(); err != nil {
					vs.logger.Error("failed to clean up storage", err)
					continue
				}
			}
		}
	})
	return vs, nil
}

func (vs *videostore) InitH264(sps, pps []byte) error {
	if vs.typ != SourceTypeH264RTPPacket {
		return errors.New("InitH264 unimplmeented")
	}
	return vs.rawSegmenter.initH264(sps, pps)
}

func (vs *videostore) WritePacket(payload []byte, pts int64, isIDR bool) error {
	if vs.typ != SourceTypeH264RTPPacket {
		return errors.New("WritePacket unimplmeented")
	}
	return vs.rawSegmenter.writePacket(payload, pts, isIDR)
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
	vs.logger.Debug("save command received")
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

// fetchFrames reads frames from the camera at the framerate interval
// and stores the decoded image in the latestFrame atomic pointer.
func (vs *videostore) fetchFrames(ctx context.Context, cam camera.Camera) {
	mimeHandler := newMimeHandler(vs.logger)
	defer mimeHandler.close()
	frameInterval := time.Second / time.Duration(vs.config.FramePoller.Framerate)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var mimeTypeReq string
			if vs.config.FramePoller.YUYV {
				mimeTypeReq = mimeTypeYUYV
			} else {
				mimeTypeReq = rutils.MimeTypeJPEG
			}
			bytes, metadata, err := cam.Image(ctx, mimeTypeReq, nil)
			if err != nil {
				vs.logger.Warn("failed to get frame from camera", err)
				time.Sleep(retryInterval * time.Second)
				continue
			}
			var frame *C.AVFrame

			mimetype, _ := rutils.CheckLazyMIMEType(metadata.MimeType)
			switch mimetype {
			case mimeTypeYUYV:
				frame, err = mimeHandler.yuyvToYUV420p(bytes)
				if err != nil {
					vs.logger.Error("failed to convert yuyv422 to yuv420p", err)
					continue
				}
			case rutils.MimeTypeJPEG, rutils.MimeTypeJPEG + "+" + rutils.MimeTypeSuffixLazy:
				frame, err = mimeHandler.decodeJPEG(bytes)
				if err != nil {
					vs.logger.Error("failed to decode jpeg", err)
					continue
				}
			default:
				vs.logger.Warn("unsupported image format", metadata.MimeType)
				continue
			}
			vs.latestFrame.Store(frame)
		}
	}
}

// processFrames grabs the latest frame, encodes, and writes to the segmenter
// which chunks video stream into clip files inside the storage directory.
func (vs *videostore) processFrames(ctx context.Context, encoder *encoder) {
	defer encoder.close()
	frameInterval := time.Second / time.Duration(vs.config.FramePoller.Framerate)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			latestFrame := vs.latestFrame.Load()
			if latestFrame == nil {
				vs.logger.Debug("latest frame is not available yet")
				continue
			}
			// NOTE(Nick S): BEGIN
			// The code in this block is wrong.
			// I've seen tests segfault with the following errors:
			// \_ [segment @ 0x14af0d710] Opening '/Users/nicksanford/code/video-store/.artifact/data/2025-02-18_15-10-39.mp4' for writing
			// DEBUG   rdk:component:camera/video-store-1      camera/camera.go:118    save command received
			// DEBUG   rdk:component:camera/video-store-1      videostore/videostore.go:270    save command received
			// DEBUG   rdk:component:camera/video-store-1      videostore/videostore.go:279    running save command asynchronously
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ [segment @ 0x14af0d710] Opening '/Users/nicksanford/code/video-store/.artifact/data/2025-02-18_15-10-49.mp4' for writing
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ SIGSEGV: segmentation violation
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ PC=0x10589fc84 m=5 sigcode=2 addr=0x7ba986502470
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ signal arrived during cgo execution
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ goroutine 129 gp=0x140005d6e00 m=5 mp=0x14000100808 [syscall]:
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ runtime.cgocall(0x105775648, 0x14000818cd8)
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_      /opt/homebrew/Cellar/go/1.23.6/libexec/src/runtime/cgocall.go:167 +0x44 fp=0x14000818ca0 sp=0x14000818c60 pc=0x104a6ebf4
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ github.com/viam-modules/video-store/videostore._Cfunc_avcodec_send_frame(0x14b80ae50, 0x14b80a9c0)
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_      _cgo_gotypes.go:1270 +0x34 fp=0x14000818cd0 sp=0x14000818ca0 pc=0x105736b44
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_ github.com/viam-modules/video-store/videostore.(*encoder).encode.func1(0x163?, 0x14b80a9c0)
			// ERROR   video-store-module.modmanager.video-storage.StdErr      pexec/managed_process.go:277
			// \_      /Users/nicksanford/code/video-store/videostore/encoder.go:178 +0x68 fp=0x14000818d10 sp=0x14000818cd0 pc=0x105738888

			// the encoder and segmenter need to be moved into a single object with a single lifetime and not pass codecCtx
			// between go objects
			result, err := encoder.encode(latestFrame)
			if err != nil {
				vs.logger.Debug("failed to encode frame", err)
				continue
			}
			if result.frameDimsChanged {
				vs.logger.Info("reinitializing segmenter due to encoder refresh")
				err = vs.segmenter.initialize(encoder.codecCtx)
				if err != nil {
					vs.logger.Debug("failed to reinitialize segmenter", err)
					// Hack that flags the encoder to reinitialize if segmenter fails to
					// ensure that encoder and segmenter inits are in sync.
					encoder.codecCtx = nil
					continue
				}
			}
			err = vs.segmenter.writeEncodedFrame(result.encodedData, result.pts, result.dts)
			if err != nil {
				vs.logger.Debug("failed to segment frame", err)
				continue
			}
			// END
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
			if err := vs.segmenter.cleanupStorage(); err != nil {
				vs.logger.Error("failed to clean up storage", err)
				continue
			}
		}
	}
}

// asyncSave command will run the concat operation in the background.
// It waits for the segment duration before running to ensure the last segment
// is written to storage before concatenation.
// TODO: (seanp) Optimize this to immediately run as soon as the current segment is completed.
func (vs *videostore) asyncSave(ctx context.Context, from, to time.Time, path string) {
	// TODO: change this to be hard coded
	segmentDur := time.Duration(vs.config.Storage.SegmentSeconds) * time.Second
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
func (vs *videostore) Close(_ context.Context) error {
	vs.logger.Infof("Close START")
	defer vs.logger.Infof("Close END")
	if vs.workers != nil {
		vs.workers.Stop()
	}
	if vs.segmenter != nil {
		vs.segmenter.close()
	}
	if vs.rawSegmenter != nil {
		vs.rawSegmenter.close()
	}
	return nil
}
