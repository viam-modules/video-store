// Package filteredvideo contains the implementation of the filtered video camera component.
package filteredvideo

import (
	"context"
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/rimage"
	"go.viam.com/rdk/rimage/transform"
	"go.viam.com/rdk/services/vision"
	rdkutils "go.viam.com/rdk/utils"
	"go.viam.com/utils"
)

// Model is the model for the filtered video camera component.
// TODO(seanp): Personal module for now, should be movied to viam module in prod.
var Model = resource.ModelNamespace("seanavery").WithFamily("camera").WithModel("filtered-video")

const (
	defaultClipLength   = 30 // seconds
	defaultStorageSize  = 10 // GB
	defaultVideoCodec   = "h264"
	defaultVideoBitrate = 1000000
	defaultVideoPreset  = "medium"
	defaultVideoFormat  = "mp4"
	defaultLogLevel     = "error"
	uploadPath          = "/.viam/video-upload/"
)

type filteredVideo struct {
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	name       resource.Name
	conf       *Config
	logger     logging.Logger
	uploadPath string

	cam    camera.Camera
	stream gostream.VideoStream
	vis    vision.Service

	workers rdkutils.StoppableWorkers

	enc *encoder
	seg *segmenter

	mu       sync.Mutex
	triggers map[string]bool
	watcher  *fsnotify.Watcher

	latestFrame atomic.Pointer[image.Image]
	lastFile    string
}

type storage struct {
	ClipLength int `json:"clip_length"`
	Size       int `json:"size"`
}

type video struct {
	Codec   string `json:"codec"`
	Bitrate int    `json:"bitrate"`
	Preset  string `json:"preset"`
	Format  string `json:"format"`
}

type cameraProperties struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	Framerate int `json:"framerate"`
}

// Config is the configuration for the filtered video camera component.
type Config struct {
	Camera  string  `json:"camera"`
	Vision  string  `json:"vision"`
	Storage storage `json:"storage"`
	Video   video   `json:"video"`

	Classifications map[string]float64 `json:"classifications,omitempty"`
	Objects         map[string]float64 `json:"objects,omitempty"`

	// TODO(seanp): Remove once camera properties are returned from camera component.
	Properties cameraProperties `json:"cam_props"`
}

func init() {
	resource.RegisterComponent(
		camera.API,
		Model,
		resource.Registration[camera.Camera, *Config]{
			Constructor: newFilteredVideo,
		})
}

func newFilteredVideo(
	ctx context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (camera.Camera, error) {
	newConf, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}

	fv := &filteredVideo{
		name:   conf.ResourceName(),
		conf:   newConf,
		logger: logger,
	}

	// Source camera that provides the frames to be processed.
	fv.cam, err = camera.FromDependencies(deps, newConf.Camera)
	if err != nil {
		return nil, err
	}

	// Vision service that provides the detections for the frames.
	fv.vis, err = vision.FromDependencies(deps, newConf.Vision)
	if err != nil {
		return nil, err
	}

	var errHandlers []gostream.ErrorHandler
	fv.stream, err = fv.cam.Stream(ctx, errHandlers...)
	if err != nil {
		return nil, err
	}

	// TODO(seanp): make this configurable
	logLevel := lookupLogID(defaultLogLevel)
	ffmppegLogLevel(logLevel)

	// TODO(seanp): Forcing h264 for now until h265 is supported.
	if newConf.Video.Codec != "h264" {
		newConf.Video.Codec = defaultVideoCodec
	}
	if newConf.Video.Bitrate == 0 {
		newConf.Video.Bitrate = defaultVideoBitrate
	}
	if newConf.Video.Preset == "" {
		newConf.Video.Preset = defaultVideoPreset
	}
	if newConf.Video.Format == "" {
		newConf.Video.Format = defaultVideoFormat
	}

	fv.enc, err = newEncoder(
		logger,
		newConf.Video.Codec,
		newConf.Video.Bitrate,
		newConf.Video.Preset,
		newConf.Properties.Width,
		newConf.Properties.Height,
		newConf.Properties.Framerate,
	)
	if err != nil {
		return nil, err
	}

	if newConf.Storage.ClipLength == 0 {
		newConf.Storage.ClipLength = defaultClipLength
	}
	if newConf.Storage.Size == 0 {
		newConf.Storage.Size = defaultStorageSize
	}
	fv.seg, err = newSegmenter(logger, fv.enc, newConf.Storage.Size, newConf.Storage.ClipLength)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	fv.watcher = watcher

	fv.triggers = make(map[string]bool)
	fv.uploadPath = getHomeDir() + uploadPath
	err = createDir(fv.uploadPath)
	if err != nil {
		return nil, err
	}

	fv.workers = rdkutils.NewStoppableWorkers(fv.processFrames, fv.processDetections, fv.deleter, fv.copier)

	// Start fsnotify watcher to listen for new files created in the storage path.
	err = watcher.Add(fv.seg.storagePath)
	if err != nil {
		return nil, err
	}

	return fv, nil
}

// Validate validates the configuration for the filtered video camera component.
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}

	if cfg.Vision == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "vision")
	}

	// TODO(seanp): Remove once camera properties are returned from camera component.
	if cfg.Properties == (cameraProperties{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "cam_props")
	}

	return []string{cfg.Camera, cfg.Vision}, nil
}

func (fv *filteredVideo) Name() resource.Name {
	return fv.name
}

func (fv *filteredVideo) DoCommand(_ context.Context, _ map[string]interface{}) (map[string]interface{}, error) {
	return nil, resource.ErrDoUnimplemented
}

func (fv *filteredVideo) Images(_ context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, errors.New("not implemented")
}

func (fv *filteredVideo) NextPointCloud(_ context.Context) (pointcloud.PointCloud, error) {
	return nil, errors.New("not implemented")
}

func (fv *filteredVideo) Projector(ctx context.Context) (transform.Projector, error) {
	return fv.cam.Projector(ctx)
}

func (fv *filteredVideo) Properties(ctx context.Context) (camera.Properties, error) {
	p, err := fv.cam.Properties(ctx)
	if err == nil {
		p.SupportsPCD = false
	}
	return p, err
}

func (fv *filteredVideo) Stream(_ context.Context, _ ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	return nil, errors.New("not implemented")
}

// processFrames reads frames from the camera, encodes, and writes to the segmenter
// which chuncks video stream into clip files inside the storage directory. This is
// meant for long term storage of video clips that are not necessarily triggered by
// detections.
// TODO(seanp): Should this be throttled to a certain FPS?
func (fv *filteredVideo) processFrames(ctx context.Context) {
	for {
		// TODO(seanp): How to gracefully exit this loop?
		select {
		case <-ctx.Done():
			return
		default:
		}
		frame, _, err := fv.stream.Next(ctx)
		if err != nil {
			fv.logger.Error("failed to get frame from camera", err)
			return
		}
		fv.latestFrame.Store(&frame)
		lazyImage, ok := frame.(*rimage.LazyEncodedImage)
		if !ok {
			fv.logger.Error("frame is not of type *rimage.LazyEncodedImage")
			return
		}
		encoded, pts, dts, err := fv.enc.encode(lazyImage.DecodedImage())
		if err != nil {
			fv.logger.Error("failed to encode frame", err)
			return
		}
		// segment frame
		err = fv.seg.writeEncodedFrame(encoded, pts, dts)
		if err != nil {
			fv.logger.Error("failed to segment frame", err)
			return
		}
	}
}

// processDetections reads frames from the camera, processes them with the vision service,
// and triggers the copier to copy the frame to upload storage if a detection is found.
func (fv *filteredVideo) processDetections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		frame := fv.latestFrame.Load()
		if frame == nil {
			continue
		}
		res, err := fv.vis.Detections(ctx, *frame, nil)
		if err != nil {
			fv.logger.Error("failed to get detections from vision service", err)
			return
		}
		for _, detection := range res {
			label := detection.Label()
			score := detection.Score()

			if threshold, exists := fv.conf.Objects[label]; exists && score > threshold {
				fv.mu.Lock()
				fv.logger.Debugf("detected %s with score %f", label, score)
				fv.triggers[label] = true
				fv.mu.Unlock()
			}
		}
	}
}

// deleter is a go routine that cleans up old clips if storage is full. It runs every
// minute and deletes the oldest clip until the storage size is below the max.
func (fv *filteredVideo) deleter(ctx context.Context) {
	// TODO(seanp): Using seconds for now, but should be minutes in prod.
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Perform the deletion of the oldest clip
			err := fv.seg.cleanupStorage()
			if err != nil {
				fv.logger.Error("failed to clean up storage", err)
				continue
			}
		}
	}
}

// copier is go routine that copies the latest frame to the upload storage directory.
// It listens for files created in the storage path. If detection triggers are found during
// the last clip window the previous clip is copied to the upload storage directory with the
// trigger keys in the filename.
func (fv *filteredVideo) copier(ctx context.Context) {
	for {
		select {
		case event, ok := <-fv.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				fv.mu.Lock()
				fv.logger.Debugf("new file created: %s", event.Name)
				if len(fv.triggers) > 0 && fv.lastFile != "" {
					filename := filepath.Base(fv.lastFile)
					var triggerKeys []string
					for key := range fv.triggers {
						triggerKeys = append(triggerKeys, key)
					}
					triggersStr := strings.Join(triggerKeys, "_")
					copyName := fmt.Sprintf("%s%s_%s", fv.uploadPath, triggersStr, filename)
					fv.logger.Debugf("copying %s to %s", fv.lastFile, copyName)
					err := copyFile(fv.lastFile, copyName)
					if err != nil {
						fv.logger.Error("failed to copy file", err)
					}
				}
				fv.lastFile = event.Name
				fv.triggers = make(map[string]bool)
				fv.mu.Unlock()
			}
		case err, ok := <-fv.watcher.Errors:
			if !ok {
				return
			}
			fv.logger.Error("error:", err)
		case <-ctx.Done():
			return
		}
	}
}

// Close closes the filtered video camera component.
// It closes the stream, workers, encoder, segmenter, and watcher.
func (fv *filteredVideo) Close(ctx context.Context) error {
	err := fv.stream.Close(ctx)
	if err != nil {
		return err
	}
	fv.workers.Stop()
	fv.enc.Close()
	fv.seg.Close()
	err = fv.watcher.Close()
	if err != nil {
		return err
	}
	return nil
}
