package filtered_video

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	// TODO(seanp): what are these?
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	name   resource.Name
	conf   *Config
	logger logging.Logger

	cam    camera.Camera
	stream gostream.VideoStream
	vis    vision.Service

	workers rdkutils.StoppableWorkers

	enc *encoder
	seg *segmenter

	uploadPath string
	triggers   map[string]bool
	watcher    *fsnotify.Watcher
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

type detect struct {
	Type      string  `json:"type"`
	Label     string  `json:"label"`
	Threshold float64 `json:"threshold"`
	Alpha     float64 `json:"alpha"`
	Timeout   int     `json:"timeout"`
}

type Config struct {
	Camera  string  `json:"camera"`
	Vision  string  `json:"vision"`
	Storage storage `json:"storage"`
	Detect  detect  `json:"detect"`
	Video   video   `json:"video"`
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

	fv.cam, err = camera.FromDependencies(deps, newConf.Camera)
	if err != nil {
		return nil, err
	}

	// TODO(seanp): Re-enable this when vision service is implemented.
	fv.vis, err = vision.FromDependencies(deps, newConf.Vision)
	if err != nil {
		return nil, err
	}

	// TODO: should i add legit error handlers?
	var errHandlers []gostream.ErrorHandler
	fv.stream, err = fv.cam.Stream(ctx, errHandlers...)
	if err != nil {
		return nil, err
	}

	// TODO(seanp): make this configurable
	logLevel := lookupLogID(defaultLogLevel)
	ffmppegLogLevel(logLevel)

	// TODO(seanp): Forcing h264 for now until h265 is supported.
	// TODO(seanp): move this to validate step?
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
	fv.enc, err = newEncoder(logger, newConf.Video.Codec, newConf.Video.Bitrate, newConf.Video.Preset)
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

	fv.workers = rdkutils.NewStoppableWorkers(fv.processFrames, fv.processDetections, fv.deleter, fv.copier)

	err = watcher.Add(fv.seg.storagePath)
	if err != nil {
		return nil, err
	}

	return fv, nil
}

func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Camera == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "camera")
	}

	// TODO(seanp): Re-enable this when vision service handler is implemented.
	if cfg.Vision == "" {
		return nil, utils.NewConfigValidationFieldRequiredError(path, "vision")
	}

	return []string{cfg.Camera, cfg.Vision}, nil
}

func (fv *filteredVideo) Name() resource.Name {
	return fv.name
}

func (fv *filteredVideo) DoCommand(ctx context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	return nil, resource.ErrDoUnimplemented
}

func (fv *filteredVideo) Images(ctx context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, errors.New("not implemented")
}

func (fv *filteredVideo) NextPointCloud(ctx context.Context) (pointcloud.PointCloud, error) {
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

func (fv *filteredVideo) Stream(ctx context.Context, errHandlers ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	return nil, errors.New("not implemented")
}

// processFrames
func (fv *filteredVideo) processFrames(ctx context.Context) {
	for {
		// TODO(seanp): How to gracefully exit this loop?
		select {
		case <-ctx.Done():
			return
		default:
		}

		// TODO(seanp): Is this safe? Should we cache the latest frame from processFrame loop instead?
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

// processDetections
func (fv *filteredVideo) processDetections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// TODO(seanp): will this interfere with processFrames?
		// If so, move to caching latest frame.
		frame, _, err := fv.stream.Next(ctx)
		if err != nil {
			fv.logger.Error("failed to get frame from camera", err)
			return
		}

		// get detections from vision service
		labels := map[string]interface{}{
			"label": fv.conf.Detect.Label,
		}
		res, err := fv.vis.Detections(ctx, frame, labels)
		if err != nil {
			fv.logger.Error("failed to get detections from vision service", err)
			return
		}

		for _, detection := range res {
			if detection.Score() > fv.conf.Detect.Threshold {
				fv.logger.Infof("detected %s with score %f", detection.Label(), detection.Score())
				// add detection to triggers
				// fv.triggers = append(fv.triggers, detection.Label())
				label := detection.Label()
				fv.triggers[label] = true
			}
		}
	}
}

// deleter Cleans up old clips if storage is full
func (fv *filteredVideo) deleter(ctx context.Context) {
	// TODO(seanp): Using seconds for now, but should be minutes in prod.
	ticker := time.NewTicker(30 * time.Second)
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

func (fv *filteredVideo) copier(ctx context.Context) {
	for {
		select {
		case event, ok := <-fv.watcher.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Create == fsnotify.Create {
				fv.logger.Infof("new file created: %s", event.Name)
				if len(fv.triggers) > 0 {
					// create file name
					filename := filepath.Base(event.Name)
					var triggerKeys []string
					for key := range fv.triggers {
						triggerKeys = append(triggerKeys, key)
					}
					triggersStr := strings.Join(triggerKeys, "_")
					copyName := fmt.Sprintf("%s%s_%s", fv.uploadPath, triggersStr, filename)
					fv.logger.Infof("copying %s to %s", event.Name, copyName)
					// copy to storage path
				}
				// clear out the triggers
				fv.triggers = make(map[string]bool)
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

// copier listens for detection events and copies clips to capture path
// func (fv *filteredVideo) copier(ctx context.Context) {
// 	ticker := time.NewTicker(10 * time.Second)
// 	defer ticker.Stop()

// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-ticker.C:
// 			if len(fv.clips) == 0 {
// 				fv.logger.Infof("no clips to copy")
// 				continue
// 			}

// 			minStartTime := fv.clips[0].start

// 			// Iterate through the clips to find the minimum start time
// 			for _, clip := range fv.clips {
// 				if clip.start.Before(minStartTime) {
// 					minStartTime = clip.start
// 				}
// 			}

// 			// get sorted list of video files
// 			files, err := getSortedFiles(fv.seg.storagePath)
// 			if err != nil {
// 				fv.logger.Error("failed to get sorted files", err)
// 				continue
// 			}

// 			// iterate through the end of the files to the beginning
// 			for i := len(files) - 1; i >= 0; i-- {
// 				// get the clip time from the file name
// 				clipTime, err := extractDateTime(files[i])
// 				if err != nil {
// 					fv.logger.Error("failed to extract date time", err)
// 					continue
// 				}
// 				// find clip time that is less than minStartTime
// 				if clipTime.Before(minStartTime) {
// 					fv.logger.Infof("copying %s", files[i])
// 					// copy the file to the capture path
// 					err := copyFile(files[i], "/home/viam/.viam/test.mp4")
// 					if err != nil {
// 						fv.logger.Error("failed to copy file", err)
// 						continue
// 					}
// 				}
// 			}

// 			// Use minStartTime as needed
// 			fv.logger.Infof("minimum start time: %v", minStartTime)
// 		}
// 	}
// }

func (fv *filteredVideo) Close(ctx context.Context) error {
	fv.stream.Close(ctx)
	fv.workers.Stop()
	fv.enc.Close()
	fv.seg.Close()
	return nil
}
