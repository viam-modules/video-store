package videostore

/*
#include "encoder.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"sync"
	"time"
	"unsafe"

	"go.viam.com/rdk/logging"
)

const (
	subsampleFactor = 2
)

type encoder struct {
	logger         logging.Logger
	framerate      int
	bitrate        int
	preset         string
	sizeGB         int
	storagePath    string
	segmentSeconds int

	cEncoderMu sync.Mutex
	cEncoder   *C.video_store_h264_encoder
}

const (
	outputPattern = "%Y-%m-%d_%H-%M-%S.mp4"
	gigabyte      = 1024 * 1024 * 1024
)

func newEncoder(
	encoderConfig EncoderConfig,
	framerate int,
	sizeGB int,
	storagePath string,
	logger logging.Logger,
) (*encoder, error) {
	// Initialize without codec context and source frame. We will spin up
	// the codec context and source frame when we get the first frame or when
	// a resize is needed.
	enc := &encoder{
		logger:         logger,
		bitrate:        defaultVideoBitrate,
		framerate:      framerate,
		preset:         encoderConfig.Preset,
		sizeGB:         sizeGB,
		storagePath:    storagePath,
		segmentSeconds: defaultSegmentSeconds,
	}

	return enc, nil
}

func (e *encoder) initialize() error {
	var cEncoder *C.video_store_h264_encoder
	e.cEncoderMu.Lock()
	defer e.cEncoderMu.Unlock()
	if e.cEncoder != nil {
		return errors.New("*encoder init called more than once")
	}

	outputPatternCStr := C.CString(e.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))

	presetCStr := C.CString(e.preset)
	defer C.free(unsafe.Pointer(presetCStr))

	e.logger.Infof("video_store_h264_encoder_init: e.segmentSeconds: %d, path: %s, bitrate: %d, framerate: %d, preset: %s",
		e.segmentSeconds, e.storagePath+"/"+outputPattern, e.bitrate, e.framerate, e.preset)
	ret := C.video_store_h264_encoder_init(
		&cEncoder,
		C.int(e.segmentSeconds),
		outputPatternCStr,
		C.int64_t(e.bitrate),
		C.int(e.framerate),
		presetCStr,
	)

	if ret != C.VIDEO_STORE_ENCODER_RESP_OK {
		err := errors.New("failed to initialize encoder")
		e.logger.Errorf("%s: %d: %s", err.Error(), ret, ffmpegError(ret))
		return err
	}
	e.cEncoder = cEncoder
	return nil
}

// encode encodes the given frame and returns the encoded data
// in bytes along with the PTS and DTS timestamps.
// PTS is calculated based on the frame count and source framerate.
// If the polling loop is not running at the source framerate, the
// PTS will lag behind actual run time.
// TODO: propagate error
func (e *encoder) encode(frame []byte, now time.Time) {
	unixMicro := now.UnixMicro()
	payloadC := C.CBytes(frame)
	defer C.free(payloadC)

	e.cEncoderMu.Lock()
	defer e.cEncoderMu.Unlock()
	if e.cEncoder == nil {
		e.logger.Errorf("encode called before init")
		return
	}
	ret := C.video_store_h264_encoder_write(
		e.cEncoder,
		C.int64_t(unixMicro),
		payloadC,
		C.size_t(len(frame)),
	)
	if ret != C.VIDEO_STORE_ENCODER_RESP_OK {
		err := errors.New("failed to write packet to encoder")
		e.logger.Errorf("%s: %d", err.Error(), ret)
		return
	}
}

func (e *encoder) close() {
	e.cEncoderMu.Lock()
	defer e.cEncoderMu.Unlock()
	if e.cEncoder == nil {
		return
	}
	ret := C.video_store_h264_encoder_close(&e.cEncoder)
	if ret != C.VIDEO_STORE_ENCODER_RESP_OK {
		err := errors.New("failed to close encoder")
		e.logger.Errorf("%s: %d", err.Error(), ret)
		return
	}
	e.cEncoder = nil
}
