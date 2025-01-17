package videostore

import (
	"bytes"
	"encoding/binary"
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

func createDummyYUYVPacket(width, height int) []byte {
	header := make([]byte, 12)
	copy(header[0:4], []byte("YUYV"))
	binary.BigEndian.PutUint32(header[4:8], uint32(width))
	binary.BigEndian.PutUint32(header[8:12], uint32(height))
	dataLen := width * height * 2
	yuyvData := make([]byte, dataLen)
	for i := 0; i < dataLen; i += 4 {
		yuyvData[i+0] = 0x80 // U
		yuyvData[i+1] = 0x10 // Y
		yuyvData[i+2] = 0x80 // V
		yuyvData[i+3] = 0x10 // Y
	}

	return append(header, yuyvData...)
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
	})

	t.Run("JPEG Decode fails with invalid JPEG bytes", func(t *testing.T) {
		frame, err := mh.decodeJPEG([]byte("invalid jpeg bytes"))
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "ailed to send packet to JPEG decoder")
		test.That(t, frame, test.ShouldBeNil)
	})
}

func TestYUYVToYUV420p(t *testing.T) {
	// create dummy YUYV bytes
	yuyvBytes := createDummyYUYVPacket(100, 100)
	logger := logging.NewLogger("test")
	mh := newMimeHandler(logger)
	t.Run("YUYV to YUV420p succeeds with valid YUYV bytes", func(t *testing.T) {
		frame, err := mh.yuyvToYUV420p(yuyvBytes)
		test.That(t, err, test.ShouldBeNil)
		test.That(t, frame, test.ShouldNotBeNil)
	})

	t.Run("YUYV to YUV420p fails with invalid header", func(t *testing.T) {
		frame, err := mh.yuyvToYUV420p([]byte("invalid yuyv bytes"))
		test.That(t, err, test.ShouldNotBeNil)
		test.That(t, err.Error(), test.ShouldContainSubstring, "missing 'YUYV' magic bytes")
		test.That(t, frame, test.ShouldBeNil)
	})
}
