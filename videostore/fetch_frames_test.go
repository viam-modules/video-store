package videostore

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"sync"
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
	// mu guards fields that may be modified mid-test while fetchFrames reads them
	// like imagesToReturn. Otherwise this test is unsafe and go test's -race flag
	// would get really upset.
	mu             sync.Mutex
	imagesToReturn []camera.NamedImage

	name        resource.Name
	returnError error
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
	m.mu.Lock()
	defer m.mu.Unlock()
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

	t.Run("Fails with multiple images when source_name unset", func(t *testing.T) {
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

		// Wait to see if any frame gets fetched (it shouldn't)
		time.Sleep(200 * time.Millisecond)

		// Verify no frame was stored (should still be empty)
		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, len(frameBytes), test.ShouldEqual, 0)
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

	t.Run("Clears stale frame when camera switches to invalid MIME type", func(t *testing.T) {
		jpegImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				jpegImage,
			},
		}

		vs := &videostore{
			config:      Config{},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 30,
			Camera:    mockCam,
		})

		// Poll until a valid frame is stored
		var storedFrameBytes []byte
		timeout := time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && len(frameBytes) > 0 {
				storedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for valid frame to be stored")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, storedFrameBytes, test.ShouldNotBeNil)
		test.That(t, len(storedFrameBytes), test.ShouldBeGreaterThan, 0)

		// Switch camera to return depth image
		depthImage, err := camera.NamedImageFromBytes([]byte("fake depth data"), "depth", rutils.MimeTypeRawDepth)
		test.That(t, err, test.ShouldBeNil)
		mockCam.mu.Lock()
		mockCam.imagesToReturn = []camera.NamedImage{depthImage}
		mockCam.mu.Unlock()

		// Poll until the stale frame is cleared
		var clearedFrameBytes []byte
		timeout = time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && frameBytes == nil {
				clearedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for stale frame to be cleared")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, clearedFrameBytes, test.ShouldBeNil)
	})

	t.Run("Clears stale frame when 0 images returned", func(t *testing.T) {
		jpegImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				jpegImage,
			},
		}

		vs := &videostore{
			config:      Config{},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 30,
			Camera:    mockCam,
		})

		// Poll until a valid frame is stored
		var storedFrameBytes []byte
		timeout := time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && len(frameBytes) > 0 {
				storedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for valid frame to be stored")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, storedFrameBytes, test.ShouldNotBeNil)
		test.That(t, len(storedFrameBytes), test.ShouldBeGreaterThan, 0)

		// Switch camera to return 0 images
		mockCam.mu.Lock()
		mockCam.imagesToReturn = []camera.NamedImage{}
		mockCam.mu.Unlock()

		// Poll until the stale frame is cleared
		var clearedFrameBytes []byte
		timeout = time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && frameBytes == nil {
				clearedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for stale frame to be cleared")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, clearedFrameBytes, test.ShouldBeNil)
	})

	t.Run("Clears stale frame when more than 1 image returned", func(t *testing.T) {
		jpegImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				jpegImage,
			},
		}

		vs := &videostore{
			config:      Config{},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 30,
			Camera:    mockCam,
		})

		// Poll until a valid frame is stored
		var storedFrameBytes []byte
		timeout := time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && len(frameBytes) > 0 {
				storedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for valid frame to be stored")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, storedFrameBytes, test.ShouldNotBeNil)
		test.That(t, len(storedFrameBytes), test.ShouldBeGreaterThan, 0)

		// Switch camera to return 2 images
		secondImage, err := camera.NamedImageFromBytes(jpegData, "depth", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		mockCam.mu.Lock()
		mockCam.imagesToReturn = []camera.NamedImage{jpegImage, secondImage}
		mockCam.mu.Unlock()

		// Poll until the stale frame is cleared
		var clearedFrameBytes []byte
		timeout = time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && frameBytes == nil {
				clearedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for stale frame to be cleared")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, clearedFrameBytes, test.ShouldBeNil)
	})

	t.Run("Clears stale frame when Images() returns error", func(t *testing.T) {
		jpegImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name: resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{
				jpegImage,
			},
		}

		vs := &videostore{
			config:      Config{},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 30,
			Camera:    mockCam,
		})

		// Poll until a valid frame is stored
		var storedFrameBytes []byte
		timeout := time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && len(frameBytes) > 0 {
				storedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for valid frame to be stored")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, storedFrameBytes, test.ShouldNotBeNil)
		test.That(t, len(storedFrameBytes), test.ShouldBeGreaterThan, 0)

		// Switch camera to return an error
		mockCam.mu.Lock()
		mockCam.returnError = errors.New("camera unavailable")
		mockCam.mu.Unlock()

		// Poll until the stale frame is cleared
		var clearedFrameBytes []byte
		timeout = time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && frameBytes == nil {
				clearedFrameBytes = frameBytes
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for stale frame to be cleared")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
		test.That(t, clearedFrameBytes, test.ShouldBeNil)
	})

	t.Run("Clears stale frame when Bytes() errors", func(t *testing.T) {
		// Build a NamedImage that will error on Bytes(): use an unsupported mime type
		// so that rimage.EncodeImage hits its default error case.
		badImg := image.NewNRGBA(image.Rect(0, 0, 1, 1))
		badNamedImage, err := camera.NamedImageFromImage(badImg, "color", "image/bogus")
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name:           resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{badNamedImage},
		}

		vs := &videostore{
			config:      Config{},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		// Pre-seed a stale frame to verify it gets cleared.
		vs.latestFrame.Store([]byte("stale"))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate: 30,
			Camera:    mockCam,
		})

		// Poll until the stale frame is cleared.
		timeout := time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			frameBytes, ok := frame.([]byte)
			if ok && frameBytes == nil {
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for stale frame to be cleared after Bytes() error")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}

		frame := vs.latestFrame.Load()
		frameBytes, ok := frame.([]byte)
		test.That(t, ok, test.ShouldBeTrue)
		test.That(t, frameBytes, test.ShouldBeNil)
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

	t.Run("Selects matching named image when source_name set", func(t *testing.T) {
		// Build two JPEGs of different sizes so their bytes differ and we can
		// verify which one was selected.
		colorBuf := new(bytes.Buffer)
		test.That(t, jpeg.Encode(colorBuf, image.NewRGBA(image.Rect(0, 0, 320, 240)), nil), test.ShouldBeNil)
		depthBuf := new(bytes.Buffer)
		test.That(t, jpeg.Encode(depthBuf, image.NewRGBA(image.Rect(0, 0, 640, 480)), nil), test.ShouldBeNil)
		test.That(t, bytes.Equal(colorBuf.Bytes(), depthBuf.Bytes()), test.ShouldBeFalse)

		colorImage, err := camera.NamedImageFromBytes(colorBuf.Bytes(), "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		depthImage, err := camera.NamedImageFromBytes(depthBuf.Bytes(), "depth", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name:           resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{colorImage, depthImage},
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
			Framerate:  10,
			Camera:     mockCam,
			SourceName: "depth",
		})

		// Poll until a frame matching the depth image is stored.
		timeout := time.After(2 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			if frameBytes, ok := frame.([]byte); ok && bytes.Equal(frameBytes, depthBuf.Bytes()) {
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for depth frame to be stored")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	})

	t.Run("Clears frame when source_name has no match", func(t *testing.T) {
		// Camera ignores the filter and returns only sources the user did not ask for.
		colorImage, err := camera.NamedImageFromBytes(jpegData, "color", rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)

		mockCam := &mockCamera{
			name:           resource.NewName(camera.API, "test-camera"),
			imagesToReturn: []camera.NamedImage{colorImage},
		}

		vs := &videostore{
			config:      Config{},
			logger:      logger,
			latestFrame: &atomic.Value{},
		}
		vs.latestFrame.Store([]byte("stale"))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go vs.fetchFrames(ctx, FramePollerConfig{
			Framerate:  30,
			Camera:     mockCam,
			SourceName: "depth",
		})

		// Poll until the stale frame is cleared.
		timeout := time.After(5 * time.Second)
		for {
			frame := vs.latestFrame.Load()
			if frameBytes, ok := frame.([]byte); ok && frameBytes == nil {
				break
			}
			select {
			case <-timeout:
				t.Fatal("timed out waiting for stale frame to be cleared")
			default:
				time.Sleep(50 * time.Millisecond)
			}
		}
	})
}

func TestPickImage(t *testing.T) {
	mustImg := func(name string) camera.NamedImage {
		ni, err := camera.NamedImageFromBytes([]byte("data-"+name), name, rutils.MimeTypeJPEG)
		test.That(t, err, test.ShouldBeNil)
		return ni
	}

	t.Run("empty source_name with single image returns it", func(t *testing.T) {
		only := mustImg("only")
		got, err := pickImage([]camera.NamedImage{only}, "")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, got.SourceName, test.ShouldEqual, "only")
	})

	t.Run("empty source_name with multiple images errors", func(t *testing.T) {
		_, err := pickImage([]camera.NamedImage{mustImg("a"), mustImg("b")}, "")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "set source_name")
	})

	t.Run("source_name selects matching image", func(t *testing.T) {
		got, err := pickImage([]camera.NamedImage{mustImg("color"), mustImg("depth")}, "depth")
		test.That(t, err, test.ShouldBeNil)
		test.That(t, got.SourceName, test.ShouldEqual, "depth")
	})

	t.Run("source_name with no match errors", func(t *testing.T) {
		_, err := pickImage([]camera.NamedImage{mustImg("color")}, "depth")
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, `"depth"`)
	})
}
