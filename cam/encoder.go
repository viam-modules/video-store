package filteredvideo

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
	videoCodec string,
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
	codecID := lookupCodecID(videoCodec)
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

	// TODO(seanp): These can be detected by fetching the intitial frame
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

	// TODO(seanp): make this calculated once instead of every frame
	ySize := frame.Bounds().Dx() * frame.Bounds().Dy()
	uSize := (frame.Bounds().Dx() / 2) * frame.Bounds().Dy()
	vSize := (frame.Bounds().Dx() / 2) * frame.Bounds().Dy()
	// TODO(seanp): directly copy into srcFrame
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

	// PTS/DTS time is equal frameCount times time_base. This assumes
	// that the processFrame routine is running at the source framerate.
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

func (e *encoder) Close() {
	C.avcodec_close(e.codecCtx)
	C.av_frame_free(&e.srcFrame)
	C.avcodec_free_context(&e.codecCtx)
}

// imageToYUV422 extracts unpadded yuv4222 bytes from iamge.Image.
// TODO(seanp): Make this fast by finding a smarter way to remove padding
// without iterating over every pixel.
func imageToYUV422(img image.Image) ([]byte, error) {
	ycbcrImg, ok := img.(*image.YCbCr)
	if !ok {
		return nil, fmt.Errorf("expected type *image.YCbCr, got %s", reflect.TypeOf(img))
	}
	ySize := ycbcrImg.Rect.Dx() * ycbcrImg.Rect.Dy()
	uSize := ySize / 2
	vSize := ySize / 2
	rawYUV := make([]byte, ySize+uSize+vSize)
	yIndex := 0
	uIndex := ySize
	vIndex := ySize + uSize
	for y := 0; y < ycbcrImg.Rect.Dy(); y++ {
		for x := 0; x < ycbcrImg.Rect.Dx(); x++ {
			yOff := y*ycbcrImg.YStride + x
			cOff := (y/2)*ycbcrImg.CStride + (x / 2)
			rawYUV[yIndex] = ycbcrImg.Y[yOff]
			yIndex++
			if x%2 == 0 {
				rawYUV[uIndex] = ycbcrImg.Cb[cOff]
				rawYUV[vIndex] = ycbcrImg.Cr[cOff]
				uIndex++
				vIndex++
			}
		}
	}
	return rawYUV, nil
}
