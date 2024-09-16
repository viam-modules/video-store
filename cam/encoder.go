package videostore

/*
#include <libavcodec/avcodec.h>
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"image"
	"reflect"
	"unsafe"

	"go.viam.com/rdk/logging"
)

type encoder struct {
	logger     logging.Logger
	codecCtx   *C.AVCodecContext
	srcFrame   *C.AVFrame
	frameCount int64
}

func newEncoder(
	logger logging.Logger,
	videoCodec codecType,
	bitrate int,
	preset string,
	width int,
	height int,
	framerate int,
) (*encoder, error) {
	enc := &encoder{
		logger:     logger,
		frameCount: 0,
	}
	codecID := lookupCodecIDByType(videoCodec)
	codec := C.avcodec_find_encoder(codecID)
	if codec == nil {
		return nil, errors.New("codec not found")
	}

	enc.codecCtx = C.avcodec_alloc_context3(codec)
	if enc.codecCtx == nil {
		return nil, errors.New("failed to allocate codec context")
	}

	enc.codecCtx.bit_rate = C.long(bitrate)
	enc.codecCtx.pix_fmt = C.AV_PIX_FMT_YUV422P
	enc.codecCtx.time_base = C.AVRational{num: 1, den: C.int(framerate)}
	enc.codecCtx.gop_size = C.int(framerate)
	enc.codecCtx.width = C.int(width)
	enc.codecCtx.height = C.int(height)

	// TODO(seanp): Do we want b frames? This could make it more complicated to split clips.
	enc.codecCtx.max_b_frames = 0
	presetCStr := C.CString(preset)
	tuneCStr := C.CString("zerolatency")
	defer C.free(unsafe.Pointer(presetCStr))
	defer C.free(unsafe.Pointer(tuneCStr))

	// The user can set the preset and tune for the encoder. This affects the
	// encoding speed and quality. See https://trac.ffmpeg.org/wiki/Encode/H.264
	// for more information.
	var opts *C.AVDictionary
	defer C.av_dict_free(&opts)
	ret := C.av_dict_set(&opts, C.CString("preset"), presetCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("av_dict_set failed: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("tune"), tuneCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("av_dict_set failed: %s", ffmpegError(ret))
	}

	ret = C.avcodec_open2(enc.codecCtx, codec, &opts)
	if ret < 0 {
		return nil, fmt.Errorf("avcodec_open2: %s", ffmpegError(ret))
	}

	srcFrame := C.av_frame_alloc()
	if srcFrame == nil {
		C.avcodec_close(enc.codecCtx)
		return nil, errors.New("could not allocate source frame")
	}
	srcFrame.width = enc.codecCtx.width
	srcFrame.height = enc.codecCtx.height
	srcFrame.format = C.int(enc.codecCtx.pix_fmt)
	enc.srcFrame = srcFrame

	return enc, nil
}

// encode encodes the given frame and returns the encoded data
// in bytes along with the PTS and DTS timestamps.
// PTS is calculated based on the frame count and source framerate.
// If the polling loop is not running at the source framerate, the
// PTS will lag behind actual run time.
func (e *encoder) encode(frame image.Image) ([]byte, int64, int64, error) {
	yuv, err := imageToYUV422(frame)
	if err != nil {
		return nil, 0, 0, err
	}

	ySize := frame.Bounds().Dx() * frame.Bounds().Dy()
	uSize := (frame.Bounds().Dx() / 2) * frame.Bounds().Dy()
	vSize := (frame.Bounds().Dx() / 2) * frame.Bounds().Dy()
	yPlane := C.CBytes(yuv[:ySize])
	uPlane := C.CBytes(yuv[ySize : ySize+uSize])
	vPlane := C.CBytes(yuv[ySize+uSize : ySize+uSize+vSize])
	defer C.free(yPlane)
	defer C.free(uPlane)
	defer C.free(vPlane)
	e.srcFrame.data[0] = (*C.uint8_t)(yPlane)
	e.srcFrame.data[1] = (*C.uint8_t)(uPlane)
	e.srcFrame.data[2] = (*C.uint8_t)(vPlane)
	e.srcFrame.linesize[0] = C.int(frame.Bounds().Dx())
	e.srcFrame.linesize[1] = C.int(frame.Bounds().Dx() / 2)
	e.srcFrame.linesize[2] = C.int(frame.Bounds().Dx() / 2)

	// Both PTS and DTS times are equal frameCount multiplied by the time_base.
	// This assumes that the processFrame routine is running at the source framerate.
	// TODO(seanp): What happens to playback if frame is dropped?
	e.srcFrame.pts = C.int64_t(e.frameCount)
	e.srcFrame.pkt_dts = e.srcFrame.pts
	ret := C.avcodec_send_frame(e.codecCtx, e.srcFrame)
	if ret < 0 {
		return nil, 0, 0, fmt.Errorf("avcodec_send_frame: %s", ffmpegError(ret))
	}
	pkt := C.av_packet_alloc()
	if pkt == nil {
		return nil, 0, 0, errors.New("could not allocate packet")
	}
	// Safe to free the packet since we copy later.
	defer C.av_packet_free(&pkt)
	ret = C.avcodec_receive_packet(e.codecCtx, pkt)
	if ret < 0 {
		return nil, 0, 0, fmt.Errorf("avcodec_receive_packet failed %s", ffmpegError(ret))
	}

	// Convert the encoded data to a Go byte slice. This is a necessary copy
	// to prevent dangling pointer in C memory. By copying to a Go bytes we can
	// allow the frame to be garbage collected automatically.
	encodedData := C.GoBytes(unsafe.Pointer(pkt.data), pkt.size)
	pts := int64(pkt.pts)
	dts := int64(pkt.dts)
	e.frameCount++
	// return encoded data

	return encodedData, pts, dts, nil
}

func (e *encoder) close() {
	C.avcodec_close(e.codecCtx)
	C.av_frame_free(&e.srcFrame)
	C.avcodec_free_context(&e.codecCtx)
}

// imageToYUV422 extracts unpadded YUV422 bytes from image.Image.
// This uses a row-wise copy of the Y, U, and V planes.
func imageToYUV422(img image.Image) ([]byte, error) {
	ycbcrImg, ok := img.(*image.YCbCr)
	if !ok {
		return nil, fmt.Errorf("expected type *image.YCbCr, got %s", reflect.TypeOf(img))
	}

	rect := ycbcrImg.Rect
	width := rect.Dx()
	height := rect.Dy()

	// Ensure width is even for YUV422 format
	if width%2 != 0 {
		return nil, fmt.Errorf("image width must be even for YUV422 format, got width=%d", width)
	}

	ySize := width * height
	halfWidth := width / 2
	uSize := halfWidth * height
	vSize := uSize

	rawYUV := make([]byte, ySize+uSize+vSize)

	for y := 0; y < height; y++ {
		ySrcStart := ycbcrImg.YOffset(rect.Min.X, rect.Min.Y+y)
		yDstStart := y * width
		copy(rawYUV[yDstStart:yDstStart+width], ycbcrImg.Y[ySrcStart:ySrcStart+width])
	}

	for y := 0; y < height; y++ {
		cSrcStart := ycbcrImg.COffset(rect.Min.X, rect.Min.Y+y)
		uDstStart := y * halfWidth
		vDstStart := y * halfWidth
		copy(rawYUV[ySize+uDstStart:ySize+uDstStart+halfWidth], ycbcrImg.Cb[cSrcStart:cSrcStart+halfWidth])
		copy(rawYUV[ySize+uSize+vDstStart:ySize+uSize+vDstStart+halfWidth], ycbcrImg.Cr[cSrcStart:cSrcStart+halfWidth])
	}

	return rawYUV, nil
}
