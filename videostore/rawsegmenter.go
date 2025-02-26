package videostore

/*
#include "rawsegmenter.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
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
	initialized    bool
	closed         bool
	maxStorageSize int64
	cRawSeg        *C.raw_seg
}

func newRawSegmenter(
	logger logging.Logger,
	typ SourceType,
	storageSize int,
	storagePath string,
	segmentSeconds int,
) (*rawSegmenter, error) {
	switch typ {
	case SourceTypeH264RTPPacket | SourceTypeH265RTPPacket:
	case SourceTypeFrame:
		fallthrough
	default:
		return nil, fmt.Errorf("unsupported SourceType %d: %s", typ, typ)
	}
	s := &rawSegmenter{
		typ:            typ,
		logger:         logger,
		storagePath:    storagePath,
		segmentSeconds: segmentSeconds,
		maxStorageSize: int64(storageSize) * gigabyte,
	}
	err := createDir(s.storagePath)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (rs *rawSegmenter) init(width, height int, extradata []byte) error {
	if len(extradata) == 0 {
		return errors.New("extradata should not be empty")
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.initialized {
		return errors.New("*rawSegmenter init called more than once")
	}

	if rs.closed {
		return errors.New("*rawSegmenter init called after close")
	}
	var cRS *C.raw_seg
	// Allocate output context for segmenter. The "segment" format is a special format
	// that allows for segmenting output files. The output pattern is a strftime pattern
	// that specifies the output file name. The pattern is set to the current time.
	outputPatternCStr := C.CString(rs.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))
	extradataCStr := C.CBytes(extradata)
	defer C.free(extradataCStr)
	var ret C.int
	switch rs.typ {
	case SourceTypeH264RTPPacket:
		ret = C.video_store_raw_seg_init_h264(
			&cRS,
			C.int(rs.segmentSeconds),
			outputPatternCStr,
			(*C.char)(extradataCStr),
			C.size_t(len(extradata)),
			C.int(width),
			C.int(height))
	case SourceTypeH265RTPPacket:
		ret = C.video_store_raw_seg_init_h265(
			&cRS,
			C.int(rs.segmentSeconds),
			outputPatternCStr,
			(*C.char)(extradataCStr),
			C.size_t(len(extradata)),
			C.int(width),
			C.int(height))
	case SourceTypeFrame:
		fallthrough
	default:
		return errors.New("invalid source type")
	}

	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		return errors.New("failed to initialize raw segmenter")
	}
	rs.cRawSeg = cRS
	rs.initialized = true

	return nil
}

func (rs *rawSegmenter) writePacket(payload []byte, pts int64, isIDR bool) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if !rs.initialized {
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
		idr)
	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		return errors.New("failed to write packet")
	}
	return nil
}

// close closes the segmenter and writes the trailer to prevent corruption
// when exiting early in the middle of a segment.
func (rs *rawSegmenter) close() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.closed {
		return
	}
	ret := C.video_store_raw_seg_close(&rs.cRawSeg)
	if ret != C.VIDEO_STORE_RAW_SEG_RESP_OK {
		rs.logger.Error("failed to close raw segmeneter")
	}
}

// cleanupStorage cleans up the storage directory by deleting the oldest files
// until the storage size is below the max.
func (rs *rawSegmenter) cleanupStorage() error {
	rs.logger.Info("cleanupStorage start")
	defer rs.logger.Info("cleanupStorage stop")
	currStorageSize, err := getDirectorySize(rs.storagePath)
	if err != nil {
		return err
	}
	if currStorageSize < rs.maxStorageSize {
		return nil
	}
	files, err := getSortedFiles(rs.storagePath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if currStorageSize < rs.maxStorageSize {
			break
		}
		rs.logger.Debugf("deleting file: %s", file)
		err := os.Remove(file)
		if err != nil {
			return err
		}
		rs.logger.Debugf("deleted file: %s", file)
		// NOTE: This is going to be super slow
		// we should speed this up
		currStorageSize, err = getDirectorySize(rs.storagePath)
		if err != nil {
			return err
		}
	}
	return nil
}
