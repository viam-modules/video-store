package videostore

/*
#include "rawsegmenter.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"go.viam.com/rdk/logging"
)

type rawSegmenter struct {
	logger         logging.Logger
	storagePath    string
	segmentSeconds int
	cRawSegMu      sync.Mutex
	cRawSeg        *C.raw_seg
}

//  -----------------
//  | State Machine |
//  -----------------
//    Uninitialized
//     |         ^
//     |         |
//   (Init)   (Close)
//     |         |
//     v         |
//     Initialized
//       |     ^
//       |     |
//       -------
//    (WritePacket)

func newRawSegmenter(
	logger logging.Logger,
	storagePath string,
	segmentSeconds int,
) (*rawSegmenter, error) {
	s := &rawSegmenter{
		logger:         logger,
		storagePath:    storagePath,
		segmentSeconds: segmentSeconds,
	}
	err := createDir(s.storagePath)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (rs *rawSegmenter) Init(codec CodecType, width, height int) error {
	if width <= 0 || height <= 0 {
		err := errors.New("both width and height must be greater than zero")
		rs.logger.Warn(err.Error())
		return err
	}

	rs.cRawSegMu.Lock()
	defer rs.cRawSegMu.Unlock()
	if rs.cRawSeg != nil {
		err := errors.New("*rawSegmenter init called more than once")
		rs.logger.Warn(err.Error())
		return err
	}

	var cRS *C.raw_seg
	// Allocate output context for segmenter. The "segment" format is a special format
	// that allows for segmenting output files. The output pattern is a strftime pattern
	// that specifies the output file name. The pattern is set to the current time.
	outputPatternCStr := C.CString(rs.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))
	var ret C.int
	switch codec {
	case CodecTypeH264:
		ret = C.video_store_raw_seg_init_h264(
			&cRS,
			C.int(rs.segmentSeconds),
			outputPatternCStr,
			C.int(width),
			C.int(height))
	case CodecTypeH265:
		ret = C.video_store_raw_seg_init_h265(
			&cRS,
			C.int(rs.segmentSeconds),
			outputPatternCStr,
			C.int(width),
			C.int(height))
	default:
		err := fmt.Errorf("rawSegmenter.init called on invalid codec %s", codec)
		rs.logger.Warn(err.Error())
		return err
	}

	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		err := errors.New("failed to initialize raw segmenter")
		rs.logger.Errorf("%s: %d: %s", err.Error(), ret, ffmpegError(ret))
		return err
	}
	rs.cRawSeg = cRS

	return nil
}

func (rs *rawSegmenter) WritePacket(payload []byte, pts, dts int64, isIDR bool) error {
	rs.cRawSegMu.Lock()
	defer rs.cRawSegMu.Unlock()
	if rs.cRawSeg == nil {
		err := errors.New("writePacket called before init")
		rs.logger.Warn(err.Error())
		return err
	}

	if len(payload) == 0 {
		err := errors.New("writePacket called with empty packet")
		rs.logger.Warn(err.Error())
		return err
	}

	payloadC := C.CBytes(payload)
	defer C.free(payloadC)

	idr := C.int(0)
	if isIDR {
		idr = C.int(1)
	}
	ret := C.video_store_raw_seg_write_packet(
		rs.cRawSeg,
		(*C.char)(payloadC),
		C.size_t(len(payload)),
		C.int64_t(pts),
		C.int64_t(dts),
		idr)
	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		err := errors.New("failed to write packet")
		rs.logger.Errorf("%s: %d", err.Error(), ret)
		return err
	}
	return nil
}

// Close closes the segmenter and writes the trailer to prevent corruption
// when exiting early in the middle of a segment.
func (rs *rawSegmenter) Close() error {
	rs.cRawSegMu.Lock()
	defer rs.cRawSegMu.Unlock()
	err := rs.close()
	if err != nil {
		rs.logger.Warnf("RawSegmenter.Close err: %s", err.Error())
		return err
	}
	return nil
}

// close must be called while the caller holds cRawSegMu
func (rs *rawSegmenter) close() error {
	if rs.cRawSeg == nil {
		return nil
	}
	ret := C.video_store_raw_seg_close(&rs.cRawSeg)
	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		return fmt.Errorf("failed to close raw segmeneter: %d", ret)
	}
	rs.cRawSeg = nil
	return nil
}
