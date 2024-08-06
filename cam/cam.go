package filtered_video

import (
	"context"
	"errors"

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
	defaultClipLength   = 30
	defaultStorageSize  = 10
	defaultVideoCodec   = "h264"
	defaultVideoBitrate = 1000000
	defaultVideoPreset  = "medium"
	defaultVideoFormat  = "mp4"
	// defaultLogLevel     = "debug"
	defaultLogLevel = "error"
)

type triggerWindow struct {
	start int64
	end   int64
}

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

	// TODO(seanp): put back in once state is required for vision service.
	// mu  sync.Mutex
	trigger triggerWindow
	clips   []triggerWindow
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

	fv.workers = rdkutils.NewStoppableWorkers(fv.processFrames, fv.processDetections)

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
	// TODO(seanp): Prototype GetVideo command here.
	fv.logger.Info("DoCommand")
	// check if fv.clips is not empty
	if len(fv.clips) > 0 {
		csvData, err := parseCSV("/home/viam/.viam/segments/seglist.csv")
		if err != nil {
			return nil, err
		}
		// inputList be the a list of the first element of the last two lines of csvData
		inputList := []string{csvData[len(csvData)-2][0], csvData[len(csvData)-1][0]}

		// TODO(seanp): Test sending video file. This is a placeholder for now.
		// We will want to concat relevant segments here.
		bytes, err := readVideoFile("/home/viam/.viam/segments/" + inputList[1])
		if err != nil {
			return nil, err
		}

		fv.logger.Infof("length of bytes: %d", len(bytes))

		fv.clips = nil

		return map[string]interface{}{
			"video": bytes,
		}, nil
	}
	// if not, get the latest clip
	// return the clip
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
				if fv.trigger.start == 0 {
					fv.logger.Info("start recording")
					fv.trigger.start = now()
				} else {
					fv.trigger.end = now()
				}
			}
		}

		if fv.trigger.end != 0 && now()-fv.trigger.end > int64(fv.conf.Detect.Timeout*1000) {
			fv.clips = append(fv.clips, fv.trigger)
			fv.logger.Info("stop recording")
			fv.trigger.start = 0
			fv.trigger.end = 0
		}

		// smooth out detections
		// cache detections
	}
}

func (fv *filteredVideo) Close(ctx context.Context) error {
	fv.stream.Close(ctx)
	fv.workers.Stop()
	fv.enc.Close()
	fv.seg.Close()
	return nil
}
