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
	typ            SourceType
	logger         logging.Logger
	storagePath    string
	segmentSeconds int
	mu             sync.Mutex
	cRawSeg        *C.raw_seg
}

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

func (rs *rawSegmenter) init(codec CodecType, width, height int) error {
	if width <= 0 || height <= 0 {
		return errors.New("both width and height must be greater than zero")
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.cRawSeg != nil {
		return errors.New("*rawSegmenter init called more than once")
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
		return fmt.Errorf("rawSegmenter.init called on invalid codec %s", codec)
	}

	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		err := errors.New("failed to initialize raw segmenter")
		rs.logger.Errorf("%s: %d: %s", err.Error(), ret, ffmpegError(ret))
		return err
	}
	rs.cRawSeg = cRS

	return nil
}

func (rs *rawSegmenter) writePacket(payload []byte, pts, dts int64, isIDR bool) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.cRawSeg == nil {
		return errors.New("writePacket called before init")
	}

	if len(payload) == 0 {
		return errors.New("writePacket called with empty packet")
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

// close closes the segmenter and writes the trailer to prevent corruption
// when exiting early in the middle of a segment.
func (rs *rawSegmenter) close() error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
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
