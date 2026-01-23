package camera

import (
	"fmt"
	"path/filepath"

	"github.com/viam-modules/video-store/videostore"
	"go.viam.com/rdk/components/camera"
	rutils "go.viam.com/rdk/utils"
	"go.viam.com/utils"
)

// Storage is the config for storage.
type Storage struct {
	SizeGB      int    `json:"size_gb"`
	UploadPath  string `json:"upload_path,omitempty"`
	StoragePath string `json:"storage_path,omitempty"`
}

// Video is the config for storge.
type Video struct {
	Codec   string `json:"codec,omitempty"`
	Bitrate int    `json:"bitrate,omitempty"`
	Preset  string `json:"preset,omitempty"`
	Format  string `json:"format,omitempty"`
}

// DirectUpload configures optional direct uploads to Viam App using env-based credentials.
type DirectUpload struct {
	Enabled                 bool     `json:"enabled,omitempty"`
	BaseURL                 string   `json:"base_url,omitempty"`
	StagingDir              string   `json:"staging_dir,omitempty"`
	DeleteAfterUpload       *bool    `json:"delete_after_upload,omitempty"`
	DefaultTags             []string `json:"default_tags,omitempty"`
	DatasetIDs              []string `json:"dataset_ids,omitempty"`
	MaxRetries              int      `json:"max_retries,omitempty"`
	InitialRetryDelayMillis int      `json:"initial_retry_delay_ms,omitempty"`
}

// Config is the configuration for the video storage camera component.
type Config struct {
	Camera       string        `json:"camera,omitempty"`
	Sync         string        `json:"sync"`
	Storage      Storage       `json:"storage"`
	Video        Video         `json:"video,omitempty"`
	Framerate    int           `json:"framerate,omitempty"`
	YUYV         bool          `json:"yuyv,omitempty"`
	DirectUpload *DirectUpload `json:"direct_upload,omitempty"`
}

// Validate validates the configuration for the video storage camera component.
func (cfg *Config) Validate(path string) ([]string, []string, error) {
	if cfg.Storage == (Storage{}) {
		return nil, nil, utils.NewConfigValidationFieldRequiredError(path, "storage")
	}
	if cfg.Storage.SizeGB == 0 {
		return nil, nil, utils.NewConfigValidationFieldRequiredError(path, "size_gb")
	}
	if cfg.Sync == "" {
		return nil, nil, utils.NewConfigValidationFieldRequiredError(path, "sync")
	}
	if cfg.Framerate < 0 {
		return nil, nil, fmt.Errorf("invalid framerate %d, must be greater than 0", cfg.Framerate)
	}

	_, err := ToFrameVideoStoreVideoConfig(cfg, "someprefix", nil)
	if err != nil {
		return nil, nil, err
	}
	// This allows for an implicit camera dependency so we do not need to explicitly
	// add the camera dependency in the config.
	if cfg.Camera != "" {
		return []string{cfg.Camera}, nil, nil
	}
	return []string{}, nil, nil
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

func applyStorageDefaults(c Storage, name string) videostore.StorageConfig {
	if c.UploadPath == "" {
		home := rutils.PlatformHomeDir()
		c.UploadPath = filepath.Join(home, defaultUploadPath, name)
	}
	if c.StoragePath == "" {
		home := rutils.PlatformHomeDir()
		c.StoragePath = filepath.Join(home, defaultStoragePath, name)
	}
	return videostore.StorageConfig{
		SizeGB:               c.SizeGB,
		OutputFileNamePrefix: name,
		UploadPath:           c.UploadPath,
		StoragePath:          c.StoragePath,
	}
}

func applyDirectUploadDefaults(c *DirectUpload, storagePath string) *videostore.DirectUploadConfig {
	if c == nil || !c.Enabled {
		return nil
	}

	deleteAfter := true
	if c.DeleteAfterUpload != nil {
		deleteAfter = *c.DeleteAfterUpload
	}

	stagingDir := c.StagingDir
	if stagingDir == "" {
		stagingDir = filepath.Join(storagePath, "direct-upload-staging")
	}

	maxRetries := c.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}

	initialDelay := c.InitialRetryDelayMillis
	if initialDelay == 0 {
		initialDelay = 1000
	}

	return &videostore.DirectUploadConfig{
		Enabled:                 true,
		BaseURL:                 c.BaseURL,
		StagingDir:              stagingDir,
		DeleteAfterUpload:       &deleteAfter,
		DefaultTags:             c.DefaultTags,
		DatasetIDs:              c.DatasetIDs,
		MaxRetries:              maxRetries,
		InitialRetryDelayMillis: initialDelay,
	}
}

// ToFrameVideoStoreVideoConfig converts a Config into a videostore.Config.
func ToFrameVideoStoreVideoConfig(
	config *Config,
	name string,
	camera camera.Camera,
) (videostore.Config, error) {
	framerate := config.Framerate
	if config.Framerate == 0 {
		framerate = defaultFramerate
	}

	storage := applyStorageDefaults(config.Storage, name)

	fvsc := videostore.Config{
		Name:         name,
		Type:         videostore.SourceTypeFrame,
		Encoder:      applyVideoEncoderDefaults(config.Video),
		Storage:      storage,
		DirectUpload: applyDirectUploadDefaults(config.DirectUpload, storage.StoragePath),
		FramePoller: videostore.FramePollerConfig{
			Framerate: framerate,
			YUYV:      config.YUYV,
			Camera:    camera,
		},
	}

	return fvsc, nil
}
