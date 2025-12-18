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
	_ []string,
	_ map[string]interface{},
) (
	[]camera.NamedImage,
	resource.ResponseMetadata,
	error,
) {
	if m.returnError != nil {
		return nil, resource.ResponseMetadata{}, m.returnError
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

	t.Run("Success with single image", func(t *testing.T) {
		namedImage, err := camera.NamedImageFromBytes(jpegData, "default", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				namedImage,
			},
		}

		vs := &videostore{
			config:      Config{},
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

	t.Run("Fails with 0 images", func(t *testing.T) {
		mockCam := &mockCamera{
			name:           resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{},
		}

		vs := &videostore{
			config:      Config{},
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

	t.Run("Succeeds with multiple images (picks first)", func(t *testing.T) {
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
			config:      Config{},
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
			config:      Config{},
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

	t.Run("Continues on Images error", func(t *testing.T) {
		mockCam := &mockCamera{
			name:        resource.NewName(camera.API, "test-camera"),
			returnError: errors.New("test error"),
		}

		vs := &videostore{
			config:      Config{},
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
