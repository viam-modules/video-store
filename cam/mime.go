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
	yuyvSrcFrame *C.AVFrame
	yuyvDstFrame *C.AVFrame
	yuyvSwCtx    *C.struct_SwsContext
}

func newMimeHandler(logger logging.Logger) *mimeHandler {
	return &mimeHandler{
		logger: logger,
	}
}

func (mh *mimeHandler) yuyvToYUV420p(frameBytes []byte, width, height int) (*C.AVFrame, error) {
	if mh.yuyvSwCtx == nil || width != int(mh.yuyvDstFrame.width) || height != int(mh.yuyvDstFrame.height) {
		if err := mh.initYUYVCtx(width, height); err != nil {
			return nil, err
		}
	}

	// Fill src frame with YUYV data bytes.
	// We use C.CBytes to allocate memory in C heap and defer free it.
	yuyvBytes := C.CBytes(frameBytes)
	defer C.free(yuyvBytes)
	mh.yuyvSrcFrame.data[0] = (*C.uint8_t)(yuyvBytes)
	mh.yuyvSrcFrame.linesize[0] = C.int(width * subsampleFactor)

	// Convert YUYV to YUV420p.
	ret := C.sws_scale(
		mh.yuyvSwCtx,
		&mh.yuyvSrcFrame.data[0],
		&mh.yuyvSrcFrame.linesize[0],
		0,
		C.int(height),
		&mh.yuyvDstFrame.data[0],
		&mh.yuyvDstFrame.linesize[0],
	)
	if ret < 0 {
		return nil, errors.New("failed to convert YUYV to YUV420p")
	}

	return mh.yuyvDstFrame, nil
}

func (mh *mimeHandler) initYUYVCtx(width, height int) error {
	mh.logger.Infof("Initializing YUYV sws context with width %d and height %d", width, height)
	if mh.yuyvSwCtx != nil {
		C.sws_freeContext(mh.yuyvSwCtx)
	}
	if mh.yuyvDstFrame != nil {
		C.av_frame_free(&mh.yuyvDstFrame)
	}
	if mh.yuyvSrcFrame != nil {
		C.av_frame_free(&mh.yuyvSrcFrame)
	}

	mh.yuyvSwCtx = C.sws_getContext(C.int(width), C.int(height), C.AV_PIX_FMT_YUYV422,
		C.int(width), C.int(height), C.AV_PIX_FMT_YUV420P,
		C.SWS_FAST_BILINEAR, nil, nil, nil)
	if mh.yuyvSwCtx == nil {
		return errors.New("failed to create YUYV to YUV420p conversion context")
	}

	mh.yuyvSrcFrame = C.av_frame_alloc()
	if mh.yuyvSrcFrame == nil {
		return errors.New("failed to allocate YUYV source frame")
	}
	mh.yuyvSrcFrame.width = C.int(width)
	mh.yuyvSrcFrame.height = C.int(height)
	mh.yuyvSrcFrame.format = C.AV_PIX_FMT_YUYV422
	// allocate buffer for YUYV data
	ret := C.av_frame_get_buffer(mh.yuyvSrcFrame, 32)
	if ret < 0 {
		return errors.New("failed to allocate buffer for YUYV source frame")
	}

	mh.yuyvDstFrame = C.av_frame_alloc()
	if mh.yuyvDstFrame == nil {
		return errors.New("failed to allocate YUV420p destination frame")
	}
	mh.yuyvDstFrame.width = C.int(width)
	mh.yuyvDstFrame.height = C.int(height)
	mh.yuyvDstFrame.format = C.AV_PIX_FMT_YUV420P
	ret = C.av_frame_get_buffer(mh.yuyvDstFrame, 32)
	if ret < 0 {
		return errors.New("failed to allocate buffer for YUV420p destination frame")
	}

	return nil
}

func (mh *mimeHandler) close() {
	if mh.yuyvSwCtx != nil {
		C.sws_freeContext(mh.yuyvSwCtx)
	}
	if mh.yuyvDstFrame != nil {
		C.av_frame_free(&mh.yuyvDstFrame)
	}
	if mh.yuyvSrcFrame != nil {
		C.av_frame_free(&mh.yuyvSrcFrame)
	}
}
