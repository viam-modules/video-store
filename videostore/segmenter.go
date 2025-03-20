package videostore

/*
#include <libavfilter/avfilter.h>
#include <libavutil/avutil.h>
#include <libavformat/avformat.h>
#include <libavutil/dict.h>
#include <libavutil/error.h>
#include <libavcodec/avcodec.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"unsafe"

	"go.viam.com/rdk/logging"
)

const (
	outputPattern = "%Y-%m-%d_%H-%M-%S.mp4"
	gigabyte      = 1024 * 1024 * 1024
)

type segmenter struct {
	logger         logging.Logger
	outCtxMu       sync.Mutex
	outCtx         *C.AVFormatContext
	stream         *C.AVStream
	frameCount     int64
	maxStorageSize int64
	storagePath    string
	clipLength     int
	format         string
}

func newSegmenter(
	logger logging.Logger,
	storageSize int,
	clipLength int,
	storagePath string,
	format string,
) (*segmenter, error) {
	// Initialize struct without stream or output context. We will initialize
	// these when we get the first frame or when a resize is needed.
	s := &segmenter{
		logger:         logger,
		outCtx:         nil,
		stream:         nil,
		frameCount:     0,
		maxStorageSize: int64(storageSize) * gigabyte,
		clipLength:     clipLength,
		storagePath:    storagePath,
		format:         format,
	}
	err := createDir(s.storagePath)
	if err != nil {
		return nil, err
	}

	return s, nil
}

// initialize takes in a codec ctx and initializes the segmenter with the codec parameters.
func (s *segmenter) initialize(encoderCtx *C.AVCodecContext) error {
	s.outCtxMu.Lock()
	defer s.outCtxMu.Unlock()
	if s.outCtx != nil {
		ret := C.av_write_trailer(s.outCtx)
		if ret < 0 {
			s.logger.Errorf("failed to write trailer", "error", ffmpegError(ret))
		}
		// This will also free the stream
		C.avformat_free_context(s.outCtx)
	}

	// Allocate output context for segmenter. The "segment" format is a special format
	// that allows for segmenting output files. The output pattern is a strftime pattern
	// that specifies the output file name. The pattern is set to the current time.
	outputPatternCStr := C.CString(s.storagePath + "/" + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))
	var fmtCtx *C.AVFormatContext
	ret := C.avformat_alloc_output_context2(&fmtCtx, nil, C.CString("segment"), outputPatternCStr)
	if ret < 0 {
		return fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}

	stream := C.avformat_new_stream(fmtCtx, nil)
	if stream == nil {
		return errors.New("failed to allocate stream")
	}
	stream.id = C.int(fmtCtx.nb_streams) - 1
	stream.time_base = encoderCtx.time_base

	// Copy codec parameters from encoder to segment stream. This is equivalent to
	// -c:v copy in ffmpeg cli
	codecpar := C.avcodec_parameters_alloc()
	defer C.avcodec_parameters_free(&codecpar)
	if ret := C.avcodec_parameters_from_context(codecpar, encoderCtx); ret < 0 {
		return fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
	}
	ret = C.avcodec_parameters_copy(stream.codecpar, codecpar)
	if ret < 0 {
		return fmt.Errorf("failed to copy codec parameters %s", ffmpegError(ret))
	}

	segmentLengthCStr := C.CString(strconv.Itoa(s.clipLength))
	segmentFormatCStr := C.CString(s.format)
	resetTimestampsCStr := C.CString("1")
	breakNonKeyFramesCStr := C.CString("1")
	strftimeCStr := C.CString("1")
	defer func() {
		C.free(unsafe.Pointer(segmentLengthCStr))
		C.free(unsafe.Pointer(segmentFormatCStr))
		C.free(unsafe.Pointer(resetTimestampsCStr))
		C.free(unsafe.Pointer(breakNonKeyFramesCStr))
		C.free(unsafe.Pointer(strftimeCStr))
	}()

	// Segment options are passed as opts to avformat_write_header. This only needs
	// to be done once, and not for every segment file.
	var opts *C.AVDictionary
	defer C.av_dict_free(&opts)
	ret = C.av_dict_set(&opts, C.CString("segment_time"), segmentLengthCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set segment_time: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("segment_format"), segmentFormatCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set segment_format: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("reset_timestamps"), resetTimestampsCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set reset_timestamps: %s", ffmpegError(ret))
	}
	// TODO(seanp): Allowing this could cause flakey playback. Remove if not needed.
	// Or, fix by adding keyframe forces on the encoder side
	ret = C.av_dict_set(&opts, C.CString("break_non_keyframes"), breakNonKeyFramesCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set break_non_keyframes: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("strftime"), strftimeCStr, 0)
	if ret < 0 {
		return fmt.Errorf("failed to set strftime: %s", ffmpegError(ret))
	}

	ret = C.avformat_write_header(fmtCtx, &opts)
	if ret < 0 {
		return fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	// Writing header overwrites the time_base, so we need to reset it.
	// TODO(seanp): Figure out why this is necessary.
	stream.time_base = encoderCtx.time_base
	stream.id = C.int(fmtCtx.nb_streams) - 1
	s.stream = stream
	s.outCtx = fmtCtx

	return nil
}

// writeEncodedFrame writes an encoded frame to the output segment file.
func (s *segmenter) writeEncodedFrame(encodedData []byte, pts, dts int64) error {
	s.outCtxMu.Lock()
	defer s.outCtxMu.Unlock()
	if s.outCtx == nil {
		return errors.New("segmenter not initialized")
	}
	pkt := C.AVPacket{
		data:         (*C.uint8_t)(C.CBytes(encodedData)),
		size:         C.int(len(encodedData)),
		stream_index: s.stream.index,
		pts:          C.int64_t(pts),
		dts:          C.int64_t(dts),
	}
	defer C.free(unsafe.Pointer(pkt.data))
	ret := C.av_interleaved_write_frame(s.outCtx, &pkt)
	if ret < 0 {
		return fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
	}
	s.frameCount++
	return nil
}

// Close closes the segmenter and writes the trailer to prevent corruption
// when exiting early in the middle of a segment.
func (s *segmenter) close() {
	s.outCtxMu.Lock()
	defer s.outCtxMu.Unlock()
	if s.outCtx == nil {
		return
	}
	ret := C.av_write_trailer(s.outCtx)
	if ret < 0 {
		s.logger.Errorf("failed to write trailer", "error", ffmpegError(ret))
	}
	C.avformat_free_context(s.outCtx)
}
