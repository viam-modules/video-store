// Package videostore contains the implementation of the video storage camera component.
package videostore

/*
#cgo pkg-config: libavcodec libavutil libswscale
#include <libavutil/frame.h>
*/
import "C"

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
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
	defaultStorageSize    = 10 // GB
	defaultVideoBitrate   = 1000000
	defaultVideoPreset    = "medium"
	defaultVideoFormat    = "mp4"
	defaultUploadPath     = ".viam/capture/video-upload"
	defaultStoragePath    = ".viam/video-storage"
	defaultLogLevel       = "error"

	maxGRPCSize           = 1024 * 1024 * 32 // bytes
	deleterInterval       = 10               // minutes
	retryInterval         = 1                // seconds
	asyncTimeout          = 60               // seconds
	numFetchFrameAttempts = 3                // iterations
	tempPath              = "/tmp"

	mimeTypeYUYV = "image/yuyv422"
)

var presets = map[string]struct{}{
	"ultrafast": struct{}{},
	"superfast": struct{}{},
	"veryfast":  struct{}{},
	"faster":    struct{}{},
	"fast":      struct{}{},
	"medium":    struct{}{},
	"slow":      struct{}{},
	"slower":    struct{}{},
	"veryslow":  struct{}{},
}

type videostore struct {
	resource.AlwaysRebuild

	name   resource.Name
	config FrameVideoStoreConfig
	logger logging.Logger

	cam         camera.Camera
	latestFrame atomic.Pointer[C.AVFrame]
	workers     *utils.StoppableWorkers

	encoder     *encoder
	mimeHandler *mimeHandler
	segmenter   *segmenter
	concater    *concater
}

type StorageConfig struct {
	SegmentSeconds int
	SizeGB         int
	UploadPath     string
	StoragePath    string
}

func (c StorageConfig) Validate() error {
	var zero StorageConfig
	if c == zero {
		return errors.New("video config can't be empty")
	}
	if c.SegmentSeconds <= 0 {
		return errors.New("segment_seconds can't be less than or equal to 0")
	}

	if c.SizeGB <= 0 {
		return errors.New("size_gb can't be less than or equal to 0")
	}

	if c.UploadPath == "" {
		return errors.New("upload_path can't be blank")
	}

	if c.StoragePath == "" {
		return errors.New("storage_path can't be blank")
	}
	return nil
}

type EncoderConfig struct {
	Bitrate int
	Preset  string
}

func (c EncoderConfig) Validate() error {
	var zero EncoderConfig
	if c == zero {
		return errors.New("video config can't be empty")
	}

	if c.Bitrate <= 0 {
		return errors.New("bitrate can't be less than or equal to 0")
	}

	if _, ok := presets[c.Preset]; !ok {
		return fmt.Errorf("preset invalid: value: %s, must be one of: %s", c.Preset, strings.Join(slices.Sorted(maps.Keys(presets)), ", "))
	}
	return nil
}

// Config is the configuration for the video storage camera component.
type Config struct {
	Camera    string        `json:"camera,omitempty"`
	Sync      string        `json:"sync"`
	Storage   StorageConfig `json:"storage"`
	Video     EncoderConfig `json:"video,omitempty"`
	Framerate int           `json:"framerate,omitempty"`
	YUYV      bool          `json:"yuyv,omitempty"`
}

func applyVideoEncoderDefaults(c EncoderConfig) EncoderConfig {
	if c.Bitrate == 0 {
		c.Bitrate = defaultVideoBitrate
	}
	if c.Preset == "" {
		c.Preset = defaultVideoPreset
	}
	return c
}

func applyStorageDefaults(c StorageConfig, name string) (StorageConfig, error) {
	var zero StorageConfig
	if c.SegmentSeconds == 0 {
		c.SegmentSeconds = defaultSegmentSeconds
	}
	if c.UploadPath == "" {
		home, err := getHomeDir()
		if err != nil {
			return zero, err
		}
		c.UploadPath = filepath.Join(home, defaultUploadPath, name)
	}
	if c.StoragePath != "" {
		home, err := getHomeDir()
		if err != nil {
			return zero, err
		}
		c.StoragePath = filepath.Join(home, defaultStoragePath, name)
	}
	return c, nil
}

func ToFrameVideoStoreVideoConfig(
	config *Config,
	name string,
	camera camera.Camera,
) (FrameVideoStoreConfig, error) {
	var zero FrameVideoStoreConfig
	framerate := config.Framerate
	if config.Framerate == 0 {
		framerate = defaultFramerate
	}

	storage, err := applyStorageDefaults(config.Storage, name)
	if err != nil {
		return zero, err
	}

	fvsc := FrameVideoStoreConfig{
		Encoder: applyVideoEncoderDefaults(config.Video),
		Storage: storage,
		Camera:  camera,
		FramePoller: FramePollerConfig{
			Framerate: framerate,
			YUYV:      config.YUYV,
		},
	}

	if err := fvsc.Validate(); err != nil {
		return zero, err
	}

	return fvsc, nil
}

// Validate validates the configuration for the video storage camera component.
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Storage == (StorageConfig{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "storage")
	}
	if cfg.Storage.SizeGB == 0 {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "size_gb")
	}
	if cfg.Sync == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "sync")
	}
	if cfg.Framerate < 0 {
		return nil, fmt.Errorf("invalid framerate %d, must be greater than 0", cfg.Framerate)
	}

	_, err := ToFrameVideoStoreVideoConfig(cfg, "", nil)
	if err != nil {
		return nil, err
	}

	// This allows for an implicit camera dependency so we do not need to explicitly
	// add the camera dependency in the config.
	if cfg.Camera != "" {
		return []string{cfg.Camera}, nil
	}
	return []string{}, nil
}

type FramePollerConfig struct {
	Framerate int
	YUYV      bool
}

type FrameVideoStoreConfig struct {
	Camera      camera.Camera
	Storage     StorageConfig
	Encoder     EncoderConfig
	FramePoller FramePollerConfig
}

func (c *FrameVideoStoreConfig) Validate() error {
	if err := c.Encoder.Validate(); err != nil {
		return err
	}

	if err := c.Storage.Validate(); err != nil {
		return err
	}

	if c.FramePoller.Framerate <= 0 {
		return errors.New("framerate can't be less than or equal to 0")
	}
	return nil
}

type SaveRequest struct {
	From     time.Time
	To       time.Time
	Metadata string
	Async    bool
}

type SaveResponse struct {
	Filename string
}

func (r *SaveRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	return nil
}

type FetchRequest struct {
	From time.Time
	To   time.Time
}

type FetchResponse struct {
	Video []byte
}

func (r *FetchRequest) Validate() error {
	if r.From.After(r.To) {
		return errors.New("'from' timestamp is after 'to' timestamp")
	}
	return nil
}

type VideoStore interface {
	Fetch(context.Context, *FetchRequest) (*FetchResponse, error)
	Save(context.Context, *SaveRequest) (*SaveResponse, error)
	Close(context.Context) error
}

// TODO(seanp): make this configurable
// TODO: move to C api
func SetLibAVLogLevel(level string) {
	ffmppegLogLevel(lookupLogID(level))
}

func NewFrameVideoStore(_ context.Context, config FrameVideoStoreConfig, logger logging.Logger) (VideoStore, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	vs := &videostore{
		logger: logger,
		config: config,
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
	cameraAvailable := config.Camera != nil
	if cameraAvailable {
		vs.mimeHandler = newMimeHandler(logger)
		vs.encoder, err = newEncoder(
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
		vs.workers = utils.NewBackgroundStoppableWorkers(vs.fetchFrames, vs.processFrames, vs.deleter)
	}

	return vs, nil
}

func (vs *videostore) Fetch(ctx context.Context, r *FetchRequest) (*FetchResponse, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	vs.logger.Debug("fetch command received")

	fetchFilePath := generateOutputFilePath(vs.name.Name, formatDateTimeToString(r.From), "", tempPath)

	if err := vs.concater.concat(r.From, r.To, fetchFilePath); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return nil, err
	}
	videoSize, err := getFileSize(fetchFilePath)
	if err != nil {
		return nil, err
	}
	if videoSize > maxGRPCSize {
		return nil, errors.New("video file size exceeds max grpc size")
	}
	videoBytes, err := readVideoFile(fetchFilePath)
	if err != nil {
		return nil, err
	}
	return &FetchResponse{Video: videoBytes}, nil
}

func (vs *videostore) Save(ctx context.Context, r *SaveRequest) (*SaveResponse, error) {
	vs.logger.Debug("save command received")
	uploadFilePath := generateOutputFilePath(vs.name.Name, formatDateTimeToString(r.From), r.Metadata, vs.config.Storage.UploadPath)
	uploadFileName := filepath.Base(uploadFilePath)
	if r.Async {
		vs.logger.Debug("running save command asynchronously")
		vs.workers.Add(func(ctx context.Context) {
			vs.asyncSave(ctx, r.From, r.To, uploadFilePath)
		})
		return &SaveResponse{Filename: uploadFileName}, nil
	}

	if err := vs.concater.concat(r.From, r.To, uploadFilePath); err != nil {
		vs.logger.Error("failed to concat files ", err)
		return nil, err
	}
	return &SaveResponse{Filename: uploadFileName}, nil
}

// DoCommand processes the commands for the video storage camera component.
func (vs *videostore) DoCommand(ctx context.Context, command map[string]interface{}) (map[string]interface{}, error) {
	cmd, ok := command["command"].(string)
	if !ok {
		return nil, errors.New("invalid command type")
	}

	switch cmd {
	// Save command is used to concatenate video clips between the given timestamps.
	// The concatenated video file is then uploaded to the cloud the upload path.
	// The response contains the name of the uploaded file.
	case "save":
		vs.logger.Debug("save command received")
		req, err := toSaveCommand(command)
		if err != nil {
			return nil, err
		}

		res, err := vs.Save(ctx, req)
		if err != nil {
			return nil, err
		}

		ret := map[string]interface{}{
			"command":  "save",
			"filename": res.Filename,
		}

		if req.Async {
			ret["status"] = "async"
		}
		return ret, nil
	case "fetch":
		vs.logger.Debug("fetch command received")
		// vs.logger.Debug("video bytes: ", len(videoBytes))
		req, err := toFetchCommand(command)
		if err != nil {
			return nil, err
		}
		res, err := vs.Fetch(ctx, req)
		if err != nil {
			return nil, err
		}
		// TODO(seanp): Do we need to encode the video bytes to base64?
		videoBytesBase64 := base64.StdEncoding.EncodeToString(res.Video)
		return map[string]interface{}{
			"command": "fetch",
			"video":   videoBytesBase64,
		}, nil
	default:
		return nil, errors.New("invalid command")
	}
}

// fetchFrames reads frames from the camera at the framerate interval
// and stores the decoded image in the latestFrame atomic pointer.
func (vs *videostore) fetchFrames(ctx context.Context) {
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
			bytes, metadata, err := vs.cam.Image(ctx, mimeTypeReq, nil)
			if err != nil {
				vs.logger.Warn("failed to get frame from camera", err)
				time.Sleep(retryInterval * time.Second)
				continue
			}
			var frame *C.AVFrame
			switch metadata.MimeType {
			case mimeTypeYUYV, mimeTypeYUYV + "+" + rutils.MimeTypeSuffixLazy:
				frame, err = vs.mimeHandler.yuyvToYUV420p(bytes)
				if err != nil {
					vs.logger.Error("failed to convert yuyv422 to yuv420p", err)
					continue
				}
			case rutils.MimeTypeJPEG, rutils.MimeTypeJPEG + "+" + rutils.MimeTypeSuffixLazy:
				frame, err = vs.mimeHandler.decodeJPEG(bytes)
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
func (vs *videostore) processFrames(ctx context.Context) {
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
			result, err := vs.encoder.encode(latestFrame)
			if err != nil {
				vs.logger.Debug("failed to encode frame", err)
				continue
			}
			if result.frameDimsChanged {
				vs.logger.Info("reinitializing segmenter due to encoder refresh")
				err = vs.segmenter.initialize(vs.encoder.codecCtx)
				if err != nil {
					vs.logger.Debug("failed to reinitialize segmenter", err)
					// Hack that flags the encoder to reinitialize if segmenter fails to
					// ensure that encoder and segmenter inits are in sync.
					vs.encoder.codecCtx = nil
					continue
				}
			}
			err = vs.segmenter.writeEncodedFrame(result.encodedData, result.pts, result.dts)
			if err != nil {
				vs.logger.Debug("failed to segment frame", err)
				continue
			}
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
			err := vs.segmenter.cleanupStorage()
			if err != nil {
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
	totalTimeout := time.Duration(asyncTimeout)*time.Second + vs.concater.segmentDur
	ctx, cancel := context.WithTimeout(ctx, totalTimeout)
	defer cancel()
	timer := time.NewTimer(vs.concater.segmentDur)
	defer timer.Stop()
	select {
	case <-timer.C:
		vs.logger.Debugf("executing concat for %s", path)
		err := vs.concater.concat(from, to, path)
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
	if vs.workers != nil {
		vs.workers.Stop()
	}
	if vs.encoder != nil {
		vs.encoder.close()
	}
	if vs.segmenter != nil {
		vs.segmenter.close()
	}
	if vs.mimeHandler != nil {
		vs.mimeHandler.close()
	}
	return nil
}
