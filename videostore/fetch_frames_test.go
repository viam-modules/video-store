package videostore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"sync/atomic"
	"testing"
	"time"

	"go.viam.com/rdk/components/camera"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	rutils "go.viam.com/rdk/utils"
	"go.viam.com/test"
)

// mockCamera implements camera.Camera for testing fetchFrames behavior.
type mockCamera struct {
	camera.Camera
	name           resource.Name
	imagesToReturn []camera.NamedImage
	returnError    error
}

func (m *mockCamera) Name() resource.Name {
	return m.name
}

func (m *mockCamera) Images(
	_ context.Context,
	filterSourceNames []string,
	_ map[string]interface{},
) (
	[]camera.NamedImage,
	resource.ResponseMetadata,
	error,
) {
	if m.returnError != nil {
		return nil, resource.ResponseMetadata{}, m.returnError
	}

	// If filter is provided, filter the images
	if len(filterSourceNames) > 0 {
		var filtered []camera.NamedImage
		for _, img := range m.imagesToReturn {
			for _, filterName := range filterSourceNames {
				if img.SourceName == filterName {
					filtered = append(filtered, img)
					break
				}
			}
		}
		return filtered, resource.ResponseMetadata{}, nil
	}

	return m.imagesToReturn, resource.ResponseMetadata{}, nil
}

// createMockJPEGImage creates a simple JPEG image for testing.
func createMockJPEGImage() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 640, 480))
	buf := new(bytes.Buffer)
	jpeg.Encode(buf, img, nil)
	return buf.Bytes()
}

func TestFetchFrames(t *testing.T) {
	logger := logging.NewTestLogger(t)
	jpegData := createMockJPEGImage()

	t.Run("Success with source_name set and 1 matching image", func(t *testing.T) {
		sourceName := "color"
		namedImage, err := camera.NamedImageFromBytes(jpegData, sourceName, rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				namedImage,
			},
		}

		vs := &videostore{
			config: Config{
				SourceName: &sourceName,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait for at least one frame to be fetched
		time.Sleep(200 * time.Millisecond)

		// Verify frame was stored
		frame := vs.latestFrame.Load()
		test.That(t, frame, test.ShouldNotBeNil)
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldBeGreaterThan, 0)
	})

	t.Run("Success without source_name and 1 image", func(t *testing.T) {
		namedImage, err := camera.NamedImageFromBytes(jpegData, "default", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				namedImage,
			},
		}

		vs := &videostore{
			config: Config{
				SourceName: nil,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait for at least one frame to be fetched
		time.Sleep(200 * time.Millisecond)

		// Verify frame was stored
		frame := vs.latestFrame.Load()
		test.That(t, frame, test.ShouldNotBeNil)
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldBeGreaterThan, 0)
	})

	t.Run("Fails without source_name and 0 images", func(t *testing.T) {
		mockCam := &mockCamera{
			name:           resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{},
		}

		vs := &videostore{
			config: Config{
				SourceName: nil,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait to see if any frame gets fetched (it shouldn't)
		time.Sleep(200 * time.Millisecond)

		// Verify no frame was stored (should still be empty)
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldEqual, 0)
	})

	t.Run("Succeeds without source_name and multiple images (picks first image)", func(t *testing.T) {
		colorImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		depthImage, err := camera.NamedImageFromBytes(jpegData, "depth", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				colorImage,
				depthImage,
			},
		}

		vs := &videostore{
			config: Config{
				SourceName: nil,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait for frame to be fetched
		time.Sleep(200 * time.Millisecond)

		// Verify frame was stored
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldBeGreaterThan, 0)
		test.That(t, frameBytes, test.ShouldResemble, jpegData)
	})

	t.Run("Fails with source_name and 0 images", func(t *testing.T) {
		sourceName := "color"
		mockCam := &mockCamera{
			name:           resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{},
		}

		vs := &videostore{
			config: Config{
				SourceName: &sourceName,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait to see if any frame gets fetched (it shouldn't)
		time.Sleep(200 * time.Millisecond)

		// Verify no frame was stored (should still be empty)
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldEqual, 0)
	})

	t.Run("Filters correctly with source_name set", func(t *testing.T) {
		sourceName := "color"
		colorImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		depthImage, err := camera.NamedImageFromBytes(jpegData, "depth", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				colorImage,
				depthImage,
			},
		}

		vs := &videostore{
			config: Config{
				SourceName: &sourceName,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait for at least one frame to be fetched
		time.Sleep(200 * time.Millisecond)

		// Verify frame was stored (filter should reduce to 1 image)
		frame := vs.latestFrame.Load()
		test.That(t, frame, test.ShouldNotBeNil)
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldBeGreaterThan, 0)
	})

	t.Run("Fails with wrong MIME type", func(t *testing.T) {
		pngImage, err := camera.NamedImageFromBytes([]byte("fake png data"), "color", rutils.MimeTypePNG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				pngImage,
			},
		}

		vs := &videostore{
			config: Config{
				SourceName: nil,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait to see if any frame gets fetched (it shouldn't)
		time.Sleep(200 * time.Millisecond)

		// Verify no frame was stored (should still be empty)
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldEqual, 0)
	})

	t.Run("Succeeds with source_name set but multiple images returned (picks first)", func(t *testing.T) {
		sourceName := "color"
		// Create images with the SAME source name to simulate a broken camera
		// that returns duplicates even when filtering
		colorImage1, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		colorImage2, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				colorImage1,
				colorImage2,
			},
		}

		vs := &videostore{
			config: Config{
				SourceName: &sourceName,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait for frame to be fetched (should pick first image)
		time.Sleep(200 * time.Millisecond)

		// Verify frame was stored (picks first image even with multiple results)
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldBeGreaterThan, 0)
		test.That(t, frameBytes, test.ShouldResemble, jpegData)
	})

	t.Run("Continues on Images error", func(t *testing.T) {
		mockCam := &mockCamera{
			name:        resource.NewName(camera.API, "test-camera"),
			returnError: errors.New("test error"),
		}

		vs := &videostore{
			config: Config{
				SourceName: nil,
			},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 10,
			Camera:    mockCam,
		})

		// Wait to see if any frame gets fetched (it shouldn't)
		time.Sleep(200 * time.Millisecond)

		// Verify no frame was stored (should still be empty)
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldEqual, 0)
	})
}

func TestGetSourceNamesFromNamedImages(t *testing.T) {
	jpegData := createMockJPEGImage()

	t.Run("Empty slice returns empty slice", func(t *testing.T) {
		result := getSourceNamesFromNamedImages([]camera.NamedImage{})
		test.That(t, len(result), test.ShouldEqual, 0)
	})

	t.Run("Single image returns single source name", func(t *testing.T) {
		colorImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		images := []camera.NamedImage{
			colorImage,
		}
		result := getSourceNamesFromNamedImages(images)
		test.That(t, len(result), test.ShouldEqual, 1)
		test.That(t, result[0], test.ShouldEqual, "color")
	})

	t.Run("Multiple images returns all source names", func(t *testing.T) {
		colorImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		depthImage, err := camera.NamedImageFromBytes(jpegData, "depth", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		infraredImage, err := camera.NamedImageFromBytes(jpegData, "infrared", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		images := []camera.NamedImage{
			colorImage,
			depthImage,
			infraredImage,
		}
		result := getSourceNamesFromNamedImages(images)
		test.That(t, len(result), test.ShouldEqual, 3)
		test.That(t, result[0], test.ShouldEqual, "color")
		test.That(t, result[1], test.ShouldEqual, "depth")
		test.That(t, result[2], test.ShouldEqual, "infrared")
	})
}
