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

type encodeResult struct {
	encodedData      []byte
	pts              int64
	dts              int64
	frameDimsChanged bool
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

func (e *encoder) initialize(width, height int) (err error) {
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
	// Defer cleanup in case of error. This will prevent side effects from
	// a partially initialized encoder.
	defer func() {
		if err != nil {
			if e.codecCtx != nil {
				C.avcodec_free_context(&e.codecCtx)
			}
			if e.srcFrame != nil {
				C.av_frame_free(&e.srcFrame)
			}
		}
	}()
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
	// e.codecCtx.pix_fmt = C.AV_PIX_FMT_YUV422P
	e.codecCtx.pix_fmt = C.AV_PIX_FMT_YUV420P
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
// func (e *encoder) encode(frame image.Image) (encodeResult, error) {
func (e *encoder) encode(frame *C.AVFrame) (encodeResult, error) {
	result := encodeResult{
		encodedData:      nil,
		pts:              0,
		dts:              0,
		frameDimsChanged: false,
	}
	if e.codecCtx == nil || frame.height != e.codecCtx.height || frame.width != e.codecCtx.width {
		e.logger.Infof("Initializing encoder with frame dimensions %dx%d", frame.width, frame.height)
		err := e.initialize(int(frame.width), int(frame.height))
		if err != nil {
			return result, err
		}
		result.frameDimsChanged = true
	}

	// Both PTS and DTS times are equal frameCount multiplied by the time_base.
	// This assumes that the processFrame routine is running at the source framerate.
	// TODO(seanp): What happens to playback if frame is dropped?
	frame.pts = C.int64_t(e.frameCount)
	frame.pkt_dts = frame.pts

	// Manually force keyframes every second, removing the need to rely on
	// gop_size or other encoder settings. This is necessary for the segmenter
	// to split the video files at keyframe boundaries.
	if e.frameCount%int64(e.codecCtx.time_base.den) == 0 {
		frame.key_frame = 1
		frame.pict_type = C.AV_PICTURE_TYPE_I
	} else {
		frame.key_frame = 0
		frame.pict_type = C.AV_PICTURE_TYPE_NONE
	}

	ret := C.avcodec_send_frame(e.codecCtx, frame)
	if ret < 0 {
		return result, fmt.Errorf("avcodec_send_frame: %s", ffmpegError(ret))
	}
	pkt := C.av_packet_alloc()
	if pkt == nil {
		return result, errors.New("could not allocate packet")
	}
	// Safe to free the packet since we copy later.
	defer C.av_packet_free(&pkt)
	ret = C.avcodec_receive_packet(e.codecCtx, pkt)
	if ret < 0 {
		return result, fmt.Errorf("avcodec_receive_packet failed %s", ffmpegError(ret))
	}

	// Convert the encoded data to a Go byte slice. This is a necessary copy
	// to prevent dangling pointer in C memory. By copying to a Go bytes we can
	// allow the frame to be garbage collected automatically.
	result.encodedData = C.GoBytes(unsafe.Pointer(pkt.data), pkt.size)
	result.pts = int64(pkt.pts)
	result.dts = int64(pkt.dts)
	e.frameCount++

	// return encoded data
	return result, nil
}

func (e *encoder) close() {
	C.avcodec_close(e.codecCtx)
	C.av_frame_free(&e.srcFrame)
	C.avcodec_free_context(&e.codecCtx)
}
