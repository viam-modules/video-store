package videostore

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"go.viam.com/rdk/components/camera"
)

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

	if err := c.FramePoller.Validate(); err != nil {
		return err
	}
	return nil
}

type StorageConfig struct {
	SegmentSeconds       int
	SizeGB               int
	OutputFileNamePrefix string
	UploadPath           string
	StoragePath          string
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

	if c.OutputFileNamePrefix == "" {
		return errors.New("output_file_name_prefix can't be blank")
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

type FramePollerConfig struct {
	Framerate int
	YUYV      bool
}

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
