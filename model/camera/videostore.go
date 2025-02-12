// Package videostore contains the implementation of the video storage camera component.
package camera

/*
#cgo pkg-config: libavcodec libavutil libswscale
#include <libavutil/frame.h>
*/
import "C"

import (
	"context"
	"encoding/base64"
	"errors"

	videostore "github.com/viam-modules/video-store"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/gostream"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/pointcloud"
	"go.viam.com/rdk/resource"
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

type component struct {
	resource.AlwaysRebuild
	name       resource.Name
	logger     logging.Logger
	videostore videostore.VideoStore
}

func newComponent(
	ctx context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (camera.Camera, error) {
	config, err := resource.NativeConfig[*Config](conf)
	if err != nil {
		return nil, err
	}

	if err := checkDeps(deps, config, logger); err != nil {
		return nil, err
	}

	// Source camera that provides the frames to be processed.
	// If camera is not available, the component will start
	// without processing frames.
	cam, err := camera.FromDependencies(deps, config.Camera)
	if err != nil {
		logger.Error("failed to get camera from dependencies, video-store will not be storing video", err)
		cam = nil
	}

	vsConfig, err := toFrameVideoStoreVideoConfig(config, conf.ResourceName().Name, cam)
	if err != nil {
		return nil, err
	}

	vs, err := videostore.NewFrameVideoStore(ctx, vsConfig, logger)
	if err != nil {
		return nil, err
	}

	videostore.SetLibAVLogLevel(defaultLogLevel)

	return &component{
		name:       conf.ResourceName(),
		videostore: vs,
		logger:     logger,
	}, nil
}

func (c *component) Name() resource.Name {
	return c.name
}

// DoCommand processes the commands for the video storage camera component.
func (c *component) DoCommand(ctx context.Context, command map[string]interface{}) (map[string]interface{}, error) {
	cmd, ok := command["command"].(string)
	if !ok {
		return nil, errors.New("invalid command type")
	}

	switch cmd {
	// Save command is used to concatenate video clips between the given timestamps.
	// The concatenated video file is then uploaded to the cloud the upload path.
	// The response contains the name of the uploaded file.
	case "save":
		c.logger.Debug("save command received")
		req, err := toSaveCommand(command)
		if err != nil {
			return nil, err
		}

		res, err := c.videostore.Save(ctx, req)
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
		c.logger.Debug("fetch command received")
		// vs.logger.Debug("video bytes: ", len(videoBytes))
		req, err := toFetchCommand(command)
		if err != nil {
			return nil, err
		}
		res, err := c.videostore.Fetch(ctx, req)
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

func (c *component) Properties(_ context.Context) (camera.Properties, error) {
	return camera.Properties{}, nil
}

// Close closes the video storage camera component.
func (c *component) Close(ctx context.Context) error {
	return c.videostore.Close(ctx)
}

// Unimplemented methods for the video storage camera component.
func (c *component) Stream(_ context.Context, _ ...gostream.ErrorHandler) (gostream.VideoStream, error) {
	return nil, errors.New("not implemented")
}

func (c *component) Image(_ context.Context, _ string, _ map[string]interface{}) ([]byte, camera.ImageMetadata, error) {
	// Return a single empty byte to satisfy the Image method
	// avoiding errors from the RenderFrame GRPC call.
	return []byte{0}, camera.ImageMetadata{}, nil
}

func (c *component) Images(_ context.Context) ([]camera.NamedImage, resource.ResponseMetadata, error) {
	return nil, resource.ResponseMetadata{}, errors.New("not implemented")
}

func (c *component) NextPointCloud(_ context.Context) (pointcloud.PointCloud, error) {
	return nil, errors.New("not implemented")
}
