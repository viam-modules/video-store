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

const (
	subsampleFactor = 2
)

type encoder struct {
	logger     logging.Logger
	codecCtx   *C.AVCodecContext
	srcFrame   *C.AVFrame
	frameCount int64
	framerate  int
	width      int
	height     int
	bitrate    int
	preset     string
}

func newEncoder(
	logger logging.Logger,
	bitrate int,
	preset string,
	framerate int,
) (*encoder, error) {
	// Initialize without codec context and source frame. We will spin up
	// the codec context and source frame when we get the first frame or when
	// a resize is needed.
	enc := &encoder{
		logger:     logger,
		codecCtx:   nil,
		srcFrame:   nil,
		bitrate:    bitrate,
		framerate:  framerate,
		width:      0,
		height:     0,
		frameCount: 0,
		preset:     preset,
	}

	return enc, nil
}

func (e *encoder) initialize(width, height int) error {
	if e.codecCtx != nil {
		C.avcodec_close(e.codecCtx)
		C.avcodec_free_context(&e.codecCtx)
		// We need to reset the frame count when reinitializing the encoder
		// in order to ensure that keyframes intervals are generated correctly.
		e.frameCount = 0
	}
	if e.srcFrame != nil {
		C.av_frame_free(&e.srcFrame)
	}
	codecID := lookupCodecIDByType(codecH264)
	codec := C.avcodec_find_encoder(codecID)
	if codec == nil {
		return errors.New("codec not found")
	}
	e.codecCtx = C.avcodec_alloc_context3(codec)
	if e.codecCtx == nil {
		return errors.New("failed to allocate codec context")
	}
	e.codecCtx.bit_rate = C.int64_t(e.bitrate)
	e.codecCtx.pix_fmt = C.AV_PIX_FMT_YUV422P
	e.codecCtx.time_base = C.AVRational{num: 1, den: C.int(e.framerate)}
	e.codecCtx.width = C.int(width)
	e.codecCtx.height = C.int(height)
	// TODO(seanp): Do we want b frames? This could make it more complicated to split clips.
	e.codecCtx.max_b_frames = 0
	presetCStr := C.CString(e.preset)
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
		return fmt.Errorf("av_dict_set failed: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("tune"), tuneCStr, 0)
	if ret < 0 {
		return fmt.Errorf("av_dict_set failed: %s", ffmpegError(ret))
	}
	ret = C.avcodec_open2(e.codecCtx, codec, &opts)
	if ret < 0 {
		return fmt.Errorf("avcodec_open2: %s", ffmpegError(ret))
	}
	srcFrame := C.av_frame_alloc()
	if srcFrame == nil {
		C.avcodec_close(e.codecCtx)
		return errors.New("could not allocate source frame")
	}
	srcFrame.width = e.codecCtx.width
	srcFrame.height = e.codecCtx.height
	srcFrame.format = C.int(e.codecCtx.pix_fmt)
	e.srcFrame = srcFrame
	return nil
}

// encode encodes the given frame and returns the encoded data
// in bytes along with the PTS and DTS timestamps.
// PTS is calculated based on the frame count and source framerate.
// If the polling loop is not running at the source framerate, the
// PTS will lag behind actual run time.
func (e *encoder) encode(frame image.Image) ([]byte, int64, int64, bool, error) {
	reinit := false
	dy, dx := frame.Bounds().Dy(), frame.Bounds().Dx()
	if e.codecCtx == nil || dy != int(e.codecCtx.height) || dx != int(e.codecCtx.width) {
		e.logger.Infof("Initializing encoder with frame dimensions %dx%d", dx, dy)
		err := e.initialize(dx, dy)
		if err != nil {
			return nil, 0, 0, reinit, err
		}
		reinit = true
	}
	yuv, err := imageToYUV422(frame)
	if err != nil {
		return nil, 0, 0, reinit, err
	}

	ySize := dx * dy
	uSize := (dx / subsampleFactor) * dy
	vSize := (dx / subsampleFactor) * dy
	yPlane := C.CBytes(yuv[:ySize])
	uPlane := C.CBytes(yuv[ySize : ySize+uSize])
	vPlane := C.CBytes(yuv[ySize+uSize : ySize+uSize+vSize])
	defer C.free(yPlane)
	defer C.free(uPlane)
	defer C.free(vPlane)
	e.srcFrame.data[0] = (*C.uint8_t)(yPlane)
	e.srcFrame.data[1] = (*C.uint8_t)(uPlane)
	e.srcFrame.data[2] = (*C.uint8_t)(vPlane)
	e.srcFrame.linesize[0] = C.int(dx)
	e.srcFrame.linesize[1] = C.int(dx / subsampleFactor)
	e.srcFrame.linesize[2] = C.int(dx / subsampleFactor)

	// Both PTS and DTS times are equal frameCount multiplied by the time_base.
	// This assumes that the processFrame routine is running at the source framerate.
	// TODO(seanp): What happens to playback if frame is dropped?
	e.srcFrame.pts = C.int64_t(e.frameCount)
	e.srcFrame.pkt_dts = e.srcFrame.pts

	// Manually force keyframes every second, removing the need to rely on
	// gop_size or other encoder settings. This is necessary for the segmenter
	// to split the video files at keyframe boundaries.
	if e.frameCount%int64(e.codecCtx.time_base.den) == 0 {
		e.srcFrame.key_frame = 1
		e.srcFrame.pict_type = C.AV_PICTURE_TYPE_I
	} else {
		e.srcFrame.key_frame = 0
		e.srcFrame.pict_type = C.AV_PICTURE_TYPE_NONE
	}

	ret := C.avcodec_send_frame(e.codecCtx, e.srcFrame)
	if ret < 0 {
		return nil, 0, 0, reinit, fmt.Errorf("avcodec_send_frame: %s", ffmpegError(ret))
	}
	pkt := C.av_packet_alloc()
	if pkt == nil {
		return nil, 0, 0, reinit, errors.New("could not allocate packet")
	}
	// Safe to free the packet since we copy later.
	defer C.av_packet_free(&pkt)
	ret = C.avcodec_receive_packet(e.codecCtx, pkt)
	if ret < 0 {
		return nil, 0, 0, reinit, fmt.Errorf("avcodec_receive_packet failed %s", ffmpegError(ret))
	}

	// Convert the encoded data to a Go byte slice. This is a necessary copy
	// to prevent dangling pointer in C memory. By copying to a Go bytes we can
	// allow the frame to be garbage collected automatically.
	encodedData := C.GoBytes(unsafe.Pointer(pkt.data), pkt.size)
	pts := int64(pkt.pts)
	dts := int64(pkt.dts)
	e.frameCount++

	// return encoded data
	return encodedData, pts, dts, reinit, nil
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
	halfWidth := width / subsampleFactor
	uSize := halfWidth * height
	vSize := uSize

	rawYUV := make([]byte, ySize+uSize+vSize)

	for y := range height {
		ySrcStart := ycbcrImg.YOffset(rect.Min.X, rect.Min.Y+y)
		cSrcStart := ycbcrImg.COffset(rect.Min.X, rect.Min.Y+y)
		yDstStart := y * width
		uDstStart := y * halfWidth
		vDstStart := y * halfWidth

		copy(rawYUV[yDstStart:yDstStart+width], ycbcrImg.Y[ySrcStart:ySrcStart+width])
		copy(rawYUV[ySize+uDstStart:ySize+uDstStart+halfWidth], ycbcrImg.Cb[cSrcStart:cSrcStart+halfWidth])
		copy(rawYUV[ySize+uSize+vDstStart:ySize+uSize+vDstStart+halfWidth], ycbcrImg.Cr[cSrcStart:cSrcStart+halfWidth])
	}

	return rawYUV, nil
}
