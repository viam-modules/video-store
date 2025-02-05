package camera

import (
	"fmt"
	"path/filepath"

	"github.com/viam-modules/video-store/videostore"
	"go.viam.com/rdk/components/camera"
	"go.viam.com/utils"
)

type Storage struct {
	SegmentSeconds int    `json:"segment_seconds,omitempty"`
	SizeGB         int    `json:"size_gb"`
	UploadPath     string `json:"upload_path,omitempty"`
	StoragePath    string `json:"storage_path,omitempty"`
}

type Video struct {
	Codec   string `json:"codec,omitempty"`
	Bitrate int    `json:"bitrate,omitempty"`
	Preset  string `json:"preset,omitempty"`
	Format  string `json:"format,omitempty"`
}

// Config is the configuration for the video storage camera component.
type Config struct {
	Camera    string  `json:"camera,omitempty"`
	Sync      string  `json:"sync"`
	Storage   Storage `json:"storage"`
	Video     Video   `json:"video,omitempty"`
	Framerate int     `json:"framerate,omitempty"`
	YUYV      bool    `json:"yuyv,omitempty"`
}

// Validate validates the configuration for the video storage camera component.
func (cfg *Config) Validate(path string) ([]string, error) {
	if cfg.Storage == (Storage{}) {
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

	_, err := ToFrameVideoStoreVideoConfig(cfg, "someprefix", nil)
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

func applyVideoEncoderDefaults(c Video) videostore.EncoderConfig {
	if c.Bitrate == 0 {
		c.Bitrate = defaultVideoBitrate
	}
	if c.Preset == "" {
		c.Preset = defaultVideoPreset
	}
	return videostore.EncoderConfig{
		Bitrate: c.Bitrate,
		Preset:  c.Preset,
	}
}

func applyStorageDefaults(c Storage, name string) (videostore.StorageConfig, error) {
	var zero videostore.StorageConfig
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
	if c.StoragePath == "" {
		home, err := getHomeDir()
		if err != nil {
			return zero, err
		}
		c.StoragePath = filepath.Join(home, defaultStoragePath, name)
	}
	return videostore.StorageConfig{
		SegmentSeconds:       c.SegmentSeconds,
		SizeGB:               c.SizeGB,
		OutputFileNamePrefix: name,
		UploadPath:           c.UploadPath,
		StoragePath:          c.StoragePath,
	}, nil
}

func ToFrameVideoStoreVideoConfig(
	config *Config,
	name string,
	camera camera.Camera,
) (videostore.Config, error) {
	var zero videostore.Config
	framerate := config.Framerate
	if config.Framerate == 0 {
		framerate = defaultFramerate
	}

	storage, err := applyStorageDefaults(config.Storage, name)
	if err != nil {
		return zero, err
	}

	fvsc := videostore.Config{
		Type:    videostore.SourceTypeFrame,
		Encoder: applyVideoEncoderDefaults(config.Video),
		Storage: storage,
		FramePoller: videostore.FramePollerConfig{
			Framerate: framerate,
			YUYV:      config.YUYV,
			Camera:    camera,
		},
	}

	if err := fvsc.Validate(); err != nil {
		return zero, err
	}

	return fvsc, nil
}
