package videostore

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.viam.com/rdk/components/camera"
)

// SourceType describes the type of video source.
type SourceType int

const (
	// SourceTypeUnknown is an invalid type.
	SourceTypeUnknown SourceType = iota
	// SourceTypeFrame is a video store that creates a video from frames.
	SourceTypeFrame
	// SourceTypeRTP is a video store that creates a video from rtp packets.
	SourceTypeRTP
	// SourceTypeReadOnly is a video store that only reads already stored segment files.
	SourceTypeReadOnly
)

func (t SourceType) String() string {
	switch t {
	case SourceTypeUnknown:
		return "VideoStoreTypeUnknown"
	case SourceTypeFrame:
		return "VideoStoreTypeFrame"
	case SourceTypeRTP:
		return "SourceTypeRTP"
	case SourceTypeReadOnly:
		return "SourceTypeReadOnly"
	default:
		return "VideoStoreTypeUnknown"
	}
}

// Config configures a videostore.
type Config struct {
	Type        SourceType
	Storage     StorageConfig
	Encoder     EncoderConfig
	FramePoller FramePollerConfig
}

// Validate returns an error if the Config is invalid.
func (c *Config) Validate() error {
	if c.Type == SourceTypeUnknown {
		return fmt.Errorf("video store type can't be %s", c.Type)
	}

	if err := c.Storage.Validate(); err != nil {
		return err
	}

	if c.Type == SourceTypeFrame {
		if err := c.Encoder.Validate(); err != nil {
			return err
		}

		if err := c.FramePoller.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// StorageConfig is the config for storage.
type StorageConfig struct {
	SizeGB               int
	OutputFileNamePrefix string
	UploadPath           string
	StoragePath          string
}

// Validate returns an error if the StorageConfig is invalid.
func (c StorageConfig) Validate() error {
	var zero StorageConfig
	if c == zero {
		return errors.New("video config can't be empty")
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

	if c.OutputFileNamePrefix == "" {
		return errors.New("output_file_name_prefix can't be blank")
	}
	return nil
}

// EncoderConfig is the config for the video encoder.
type EncoderConfig struct {
	Bitrate int
	Preset  string
}

// Validate returns an error if the EncoderConfig is invalid.
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

// FramePollerConfig is the config for the frame poller.
type FramePollerConfig struct {
	Camera    camera.Camera
	Framerate int
	YUYV      bool
}

// Validate returns an error if the	FramePollerConfig is invalid.
func (c FramePollerConfig) Validate() error {
	var zero FramePollerConfig
	if c == zero {
		return errors.New("frame_poller config can't be empty")
	}

	if c.Framerate <= 0 {
		return errors.New("framerate can't be less than or equal to 0")
	}

	return nil
}
