package videostore

/*
#include <libavcodec/avcodec.h>
#include <libavutil/frame.h>
#include <libswscale/swscale.h>
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <stdlib.h>
*/
import "C"

import (
	"errors"

	"go.viam.com/rdk/logging"
)

type mimeHandler struct {
	logger       logging.Logger
	jpegDstFrame *C.AVFrame
	jpegCodecCtx *C.AVCodecContext
}

func newMimeHandler(logger logging.Logger) *mimeHandler {
	return &mimeHandler{
		logger: logger,
	}
}

func (mh *mimeHandler) initJPEGDecoder() error {
	mh.logger.Infof("initializing JPEG decoder")
	if mh.jpegCodecCtx != nil {
		C.avcodec_free_context(&mh.jpegCodecCtx)
	}
	if mh.jpegDstFrame != nil {
		C.av_frame_free(&mh.jpegDstFrame)
	}
	codec := C.avcodec_find_decoder(C.AV_CODEC_ID_MJPEG)
	if codec == nil {
		return errors.New("failed to find JPEG codec")
	}
	mh.jpegCodecCtx = C.avcodec_alloc_context3(codec)
	if mh.jpegCodecCtx == nil {
		return errors.New("failed to allocate JPEG codec context")
	}
	mh.jpegCodecCtx.pix_fmt = C.AV_PIX_FMT_YUV420P
	ret := C.avcodec_open2(mh.jpegCodecCtx, codec, nil)
	if ret < 0 {
		return errors.New("failed to open JPEG codec")
	}

	return nil
}

func (mh *mimeHandler) decodeJPEG(frameBytes []byte) (*C.AVFrame, error) {
	// fill a jpeg pkt with the frame bytes
	dataPtr := C.CBytes(frameBytes)
	defer C.free(dataPtr)
	pkt := C.AVPacket{
		data: (*C.uint8_t)(dataPtr),
		size: C.int(len(frameBytes)),
	}
	if mh.jpegCodecCtx == nil {
		if err := mh.initJPEGDecoder(); err != nil {
			return nil, err
		}
	}
	if mh.jpegCodecCtx == nil {
		return nil, errors.New("JPEG decoder not initialized")
	}
	// Allocate the destination frame if it does not already allocated
	if mh.jpegDstFrame == nil {
		mh.jpegDstFrame = C.av_frame_alloc()
		if mh.jpegDstFrame == nil {
			return nil, errors.New("could not allocate destination frame")
		}
	}
	// The mjpeg decoder can figure out width and height from the frame bytes.
	// We don't need to pass width and height to initJPEGDecoder and it can
	// recover from a change in resolution.
	ret := C.avcodec_send_packet(mh.jpegCodecCtx, &pkt)
	if ret < 0 {
		return nil, errors.New("failed to send packet to JPEG decoder")
	}
	// Receive frame will allocate the frame buffer so we do not need to
	// manually call av_frame_get_buffer.
	ret = C.avcodec_receive_frame(mh.jpegCodecCtx, mh.jpegDstFrame)
	if ret < 0 {
		return nil, errors.New("failed to receive frame from JPEG decoder")
	}

	return mh.jpegDstFrame, nil
}

func (mh *mimeHandler) close() {
}
