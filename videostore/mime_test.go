package videostore

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

func createDummyJPEG() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for x := range 100 {
		for y := range 100 {
			img.Set(x, y, color.RGBA{255, 0, 0, 255}) // Red color
		}
	}
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, nil)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func TestJPEGDecode(t *testing.T) {
	jpegBytes, err := createDummyJPEG()
	test.That(t, err, test.ShouldBeNil)
	logger := logging.NewLogger("test")
	mh := newMimeHandler(logger)
	t.Run("JPEG Decode succeeds with valid JPEG bytes", func(t *testing.T) {
		frame, err := mh.decodeJPEG(jpegBytes)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, frame, test.ShouldNotBeNil)
		test.That(t, frame.data[0], test.ShouldNotBeNil)
	})

	t.Run("JPEG Decode fails with invalid JPEG bytes", func(t *testing.T) {
		frame, err := mh.decodeJPEG([]byte("invalid jpeg bytes"))
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "failed to send packet to JPEG decoder")
		test.That(t, frame, test.ShouldBeNil)
	})
}
