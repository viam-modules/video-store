package videostore

/*
#include "rawsegmenter.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"

	"github.com/bluenviron/mediacommon/pkg/codecs/h264"
	"go.viam.com/rdk/logging"
)

type rawSegmenter struct {
	logger         logging.Logger
	storagePath    string
	segmentSeconds int
	mu             sync.Mutex
	initialized    bool
	closed         bool
	cRawSeg        *C.raw_seg_h264
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

func (rs *rawSegmenter) initH264(sps, pps []byte) error {
	if len(sps) == 0 || len(pps) == 0 {
		return errors.New("both sps & pps must not be empty")
	}

	var hsps h264.SPS
	if err := hsps.Unmarshal(sps); err != nil {
		return err
	}
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.initialized {
		return errors.New("*rawSegmenter init called more than once")
	}

	if rs.closed {
		return errors.New("*rawSegmenter init called after close")
	}

	// Set up the extradata for the codec context. This allows the muxer to know how to
	// decode the stream without having to read the SPS/PPS from the stream.
	extradata, err := buildAVCExtradata(sps, pps)
	if err != nil {
		rs.logger.Error("failed to build extradata: ", err)
		return err
	}
	var cRS *C.raw_seg_h264
	// Allocate output context for segmenter. The "segment" format is a special format
	// that allows for segmenting output files. The output pattern is a strftime pattern
	// that specifies the output file name. The pattern is set to the current time.
	outputPatternCStr := C.CString(rs.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))
	extradataCStr := C.CBytes(extradata)
	defer C.free(extradataCStr)
	ret := C.video_store_raw_seg_init_h264(
		&cRS,
		C.int(rs.segmentSeconds),
		outputPatternCStr,
		(*C.char)(extradataCStr),
		C.size_t(len(extradata)),
		C.int(hsps.Width()),
		C.int(hsps.Height()))
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
	if rs.initialized {
		return errors.New("writePacket called before initH264")
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
	ret := C.video_store_raw_seg_write_h264_packet(
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

/*
	aligned(8) class AVCDecoderConfigurationRecord {
		   unsigned int(8) configurationVersion = 1;
		   unsigned int(8) AVCProfileIndication;
		   unsigned int(8) profile_compatibility;
		   unsigned int(8) AVCLevelIndication;
		   bit(6) reserved = ‘111111’b;
		   unsigned int(2) lengthSizeMinusOne;
		   bit(3) reserved = ‘111’b;
		   unsigned int(5) numOfSequenceParameterSets;
		   for (i=0; i< numOfSequenceParameterSets;  i++) {
		      unsigned int(16) sequenceParameterSetLength ;
		  bit(8*sequenceParameterSetLength) sequenceParameterSetNALUnit;
		 }
		   unsigned int(8) numOfPictureParameterSets;
		   for (i=0; i< numOfPictureParameterSets;  i++) {
		  unsigned int(16) pictureParameterSetLength;
		  bit(8*pictureParameterSetLength) pictureParameterSetNALUnit;
		 }
		}
*/
// buildAVCExtradata builds the AVCDecoderConfigurationRecord extradata from the SPS and PPS data.
// The SPS and PPS data are expected to be packed into the format listed above.
func buildAVCExtradata(sps, pps []byte) ([]byte, error) {
	if len(sps) < 4 || len(pps) < 1 {
		return nil, errors.New("invalid SPS/PPS data")
	}
	extradata := []byte{}
	// configurationVersion
	extradata = append(extradata, 1)
	// AVCProfileIndication, profile_compatibility, AVCLevelIndication (from SPS)
	extradata = append(extradata, sps[1], sps[2], sps[3])
	// 6 bits reserved (111111) + 2 bits lengthSizeMinusOne (3 for 4 bytes)
	//nolint:mnd
	extradata = append(extradata, 0xFF)
	// 3 bits reserved (111) + 5 bits numOfSequenceParameterSets (usually 1)
	//nolint:mnd
	extradata = append(extradata, 0xE1)
	// SPS length (2 bytes big-endian)
	spsLen := uint16(len(sps))
	//nolint:mnd
	extradata = append(extradata, byte(spsLen>>8), byte(spsLen&0xff))
	// SPS data
	extradata = append(extradata, sps...)
	// Number of Picture Parameter Sets (usually 1)
	extradata = append(extradata, 1)
	// PPS length (2 bytes big-endian)
	ppsLen := uint16(len(pps))
	//nolint:mnd
	extradata = append(extradata, byte(ppsLen>>8), byte(ppsLen&0xff))
	// PPS data
	extradata = append(extradata, pps...)

	// hexdump := hex.Dump(extradata)
	// fmt.Println("extradata: \n", hexdump)
	return extradata, nil
}
