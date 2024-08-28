// Package filteredvideo contains the implementation of the filtered video camera component.
package filteredvideo

import (
	"context"
	"errors"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/rimage"
	"go.viam.com/rdk/rimage/transform"
	rdkutils "go.viam.com/rdk/utils"
	"go.viam.com/utils"
)

// Model is the model for the filtered video camera component.
// TODO(seanp): Personal module for now, should be movied to viam module in prod.
var Model = resource.ModelNamespace("seanavery").WithFamily("video").WithModel("storage")

const (
	defaultSegmentSeconds = 30 // seconds
	defaultStorageSize    = 10 // GB
	defaultVideoCodec     = "h264"
	defaultVideoBitrate   = 1000000
	defaultVideoPreset    = "medium"
	defaultVideoFormat    = "mp4"
	defaultLogLevel       = "error"
	defaultUploadPath     = "/.viam/video-upload/"
	defaultStoragePath    = "/.viam/video-storage/"
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

	workers rdkutils.StoppableWorkers

	enc *encoder
	seg *segmenter
}

type storage struct {
	SegmentSeconds int    `json:"segment_seconds"`
	SizeGB         int    `json:"size_gb"`
	UploadPath     string `json:"upload_path"`
	StoragePath    string `json:"storage_path"`
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
	Storage storage `json:"storage"`
	Video   video   `json:"video"`

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

	var errHandlers []gostream.ErrorHandler
	fv.stream, err = fv.cam.Stream(ctx, errHandlers...)
	if err != nil {
		return nil, err
	}

	// TODO(seanp): make this configurable
	// logLevel := lookupLogID(defaultLogLevel)
	logLevel := lookupLogID("debug")
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

	if newConf.Storage.SegmentSeconds == 0 {
		newConf.Storage.SegmentSeconds = defaultSegmentSeconds
	}
	if newConf.Storage.SizeGB == 0 {
		newConf.Storage.SizeGB = defaultStorageSize
	}
	if newConf.Storage.UploadPath == "" {
		newConf.Storage.UploadPath = getHomeDir() + defaultUploadPath
	}
	if newConf.Storage.StoragePath == "" {
		newConf.Storage.StoragePath = getHomeDir() + defaultStoragePath
	}
	fv.seg, err = newSegmenter(logger, fv.enc, newConf.Storage.SizeGB, newConf.Storage.SegmentSeconds, newConf.Storage.StoragePath)
	if err != nil {
		return nil, err
	}

	fv.uploadPath = newConf.Storage.UploadPath
	err = createDir(fv.uploadPath)
	if err != nil {
		return nil, err
	}

	fv.workers = rdkutils.NewStoppableWorkers(fv.processFrames, fv.deleter)

	return fv, nil
}

// Validate validates the configuration for the filtered video camera component.
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}

	// TODO(seanp): Remove once camera properties are returned from camera component.
	if cfg.Properties == (cameraProperties{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "cam_props")
	}

	return []string{cfg.Camera}, nil
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
	return nil
}
