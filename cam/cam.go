// Package videostore contains the implementation of the video storage camera component.
package videostore

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/rimage"
	"go.viam.com/utils"
)

// Model is the model for the video storage camera component.
var Model = resource.ModelNamespace("viam").WithFamily("video").WithModel("storage")

const (
	// Default values for the video storage camera component.
	defaultSegmentSeconds = 30 // seconds
	defaultStorageSize    = 10 // GB
	defaultVideoCodec     = codecH264
	defaultVideoBitrate   = 1000000
	defaultVideoPreset    = "medium"
	defaultVideoFormat    = "mp4"
	defaultUploadPath     = ".viam/capture/video-upload"
	defaultStoragePath    = ".viam/video-storage"

	defaultLogLevel = "info"
	deleterInterval = 10 // minutes
)

type videostore struct {
	resource.AlwaysRebuild

	name   resource.Name
	conf   *Config
	logger logging.Logger

	cam    camera.Camera
	stream gostream.VideoStream

	workers *utils.StoppableWorkers

	enc  *encoder
	seg  *segmenter
	conc *concater

	storagePath string
	uploadPath  string
}

type storage struct {
	SegmentSeconds int    `json:"segment_seconds,omitempty"`
	SizeGB         int    `json:"size_gb"`
	UploadPath     string `json:"upload_path,omitempty"`
	StoragePath    string `json:"storage_path,omitempty"`
}

type video struct {
	Codec   string `json:"codec,omitempty"`
	Bitrate int    `json:"bitrate,omitempty"`
	Preset  string `json:"preset,omitempty"`
	Format  string `json:"format,omitempty"`
}

type cameraProperties struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	Framerate int `json:"framerate"`
}

// Config is the configuration for the video storage camera component.
type Config struct {
	Camera  string  `json:"camera"`
	Storage storage `json:"storage"`
	Video   video   `json:"video,omitempty"`

	// TODO(seanp): Remove once camera properties are returned from camera component.
	Properties cameraProperties `json:"cam_props"`
}

func init() {
	resource.RegisterComponent(
		camera.API,
		Model,
		resource.Registration[camera.Camera, *Config]{
			Constructor: newvideostore,
		})
}

func newvideostore(
	ctx context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (camera.Camera, error) {
	newConf, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}

	vs := &videostore{
		name:   conf.ResourceName(),
		conf:   newConf,
		logger: logger,
	}

	// Source camera that provides the frames to be processed.
	vs.cam, err = camera.FromDependencies(deps, newConf.Camera)
	if err != nil {
		return nil, err
	}

	var errHandlers []gostream.ErrorHandler
	vs.stream, err = vs.cam.Stream(ctx, errHandlers...)
	if err != nil {
		return nil, err
	}

	// TODO(seanp): make this configurable
	logLevel := lookupLogID(defaultLogLevel)
	ffmppegLogLevel(logLevel)

	// Create encoder to handle encoding of frames.
	// TODO(seanp): Forcing h264 for now until h265 is supported.
	if parseCodecType(newConf.Video.Codec) != codecH264 {
		newConf.Video.Codec = defaultVideoCodec.String()
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
	vs.enc, err = newEncoder(
		logger,
		parseCodecType(newConf.Video.Codec),
		newConf.Video.Bitrate,
		newConf.Video.Preset,
		newConf.Properties.Width,
		newConf.Properties.Height,
		newConf.Properties.Framerate,
	)
	if err != nil {
		return nil, err
	}

	// Create segmenter to handle segmentation of video stream into clips.
	if newConf.Storage.SegmentSeconds == 0 {
		newConf.Storage.SegmentSeconds = defaultSegmentSeconds
	}
	if newConf.Storage.UploadPath == "" {
		newConf.Storage.UploadPath = filepath.Join(getHomeDir(), defaultUploadPath, vs.name.Name)
	}
	if newConf.Storage.StoragePath == "" {
		newConf.Storage.StoragePath = filepath.Join(getHomeDir(), defaultStoragePath, vs.name.Name)
	}
	vs.storagePath = newConf.Storage.StoragePath
	vs.seg, err = newSegmenter(
		logger,
		vs.enc,
		newConf.Storage.SizeGB,
		newConf.Storage.SegmentSeconds,
		newConf.Storage.StoragePath,
	)
	if err != nil {
		return nil, err
	}

	// Create concater to handle concatenation of video clips when requested.
	vs.uploadPath = newConf.Storage.UploadPath
	err = createDir(vs.uploadPath)
	if err != nil {
		return nil, err
	}
	vs.conc, err = newConcater(logger, vs.storagePath, vs.uploadPath, vs.name.Name, newConf.Storage.SegmentSeconds)
	if err != nil {
		return nil, err
	}

	// Start workers to process frames and clean up storage.
	vs.workers = utils.NewBackgroundStoppableWorkers(vs.processFrames, vs.deleter)

	return vs, nil
}

// Validate validates the configuration for the video storage camera component.
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}
	// Check Storage
	if cfg.Storage == (storage{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "storage")
	}
	if cfg.Storage.SizeGB == 0 {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "size_gb")
	}
	// TODO(seanp): Remove once camera properties are returned from camera component.
	if cfg.Properties == (cameraProperties{}) {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "cam_props")
	}

	return []string{cfg.Camera}, nil
}

func (vs *videostore) Name() resource.Name {
	return vs.name
}

// DoCommand processes the commands for the video storage camera component.
func (vs *videostore) DoCommand(_ context.Context, command map[string]interface{}) (map[string]interface{}, error) {
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
		from, to, metadata, err := validateSaveCommand(command)
		if err != nil {
			return nil, err
		}
		uploadFilePath, err := vs.conc.concat(from, to, metadata)
		if err != nil {
			vs.logger.Error("failed to concat files ", err)
			return nil, err
		}
		uploadFileName := filepath.Base(uploadFilePath)
		return map[string]interface{}{
			"command": "save",
			"file":    uploadFileName,
		}, nil
	case "fetch":
		vs.logger.Debug("fetch command received")
		return nil, resource.ErrDoUnimplemented
	default:
		return nil, errors.New("invalid command")
	}
}

func (vs *videostore) Properties(ctx context.Context) (camera.Properties, error) {
	p, err := vs.cam.Properties(ctx)
	if err == nil {
		p.SupportsPCD = false
	}
	return p, err
}

// processFrames reads frames from the camera, encodes, and writes to the segmenter
// which chunks video stream into clip files inside the storage directory.
// TODO(seanp): Should this be throttled to a certain FPS?
func (vs *videostore) processFrames(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		frame, _, err := vs.stream.Next(ctx)
		if err != nil {
			vs.logger.Error("failed to get frame from camera", err)
			return
		}
		lazyImage, ok := frame.(*rimage.LazyEncodedImage)
		if !ok {
			vs.logger.Error("frame is not of type *rimage.LazyEncodedImage")
			return
		}
		encoded, pts, dts, err := vs.enc.encode(lazyImage.DecodedImage())
		if err != nil {
			vs.logger.Error("failed to encode frame", err)
			return
		}
		// segment frame
		err = vs.seg.writeEncodedFrame(encoded, pts, dts)
		if err != nil {
			vs.logger.Error("failed to segment frame", err)
			return
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
			err := vs.seg.cleanupStorage()
			if err != nil {
				vs.logger.Error("failed to clean up storage", err)
				continue
			}
		}
	}
}

// Close closes the video storage camera component.
func (vs *videostore) Close(ctx context.Context) error {
	err := vs.stream.Close(ctx)
	if err != nil {
		vs.logger.Error("failed to close stream", err)
	}
	vs.workers.Stop()
	vs.enc.close()
	vs.seg.close()
	vs.conc.close()
	return nil
}

// Unimplemented methods for the video storage camera component.
func (vs *videostore) Stream(_ context.Context, _ ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	return nil, errors.New("not implemented")
}

func (vs *videostore) Images(_ context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, errors.New("not implemented")
}

func (vs *videostore) NextPointCloud(_ context.Context) (pointcloud.PointCloud, error) {
	return nil, errors.New("not implemented")
}
