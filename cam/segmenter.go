package filtered_video

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
	"os"
	"unsafe"

	"go.viam.com/rdk/logging"
)

const (
	outputDirectory = "/.viam/video-storage/"
	outputPattern   = outputDirectory + "%Y-%m-%d_%H-%M-%S.mp4"
)

type segmenter struct {
	logger         logging.Logger
	outCtx         *C.AVFormatContext
	stream         *C.AVStream
	encoder        *encoder
	frameCount     int64
	maxStorageSize int64
	storagePath    string
}

func newSegmenter(
	logger logging.Logger,
	enc *encoder,
	storageSize int,
	clipLength int,
) (*segmenter, error) {
	s := &segmenter{
		logger:  logger,
		encoder: enc,
	}
	// TOOD(seanp): MB for testing, should be GB in prod.
	s.maxStorageSize = int64(storageSize) * 1024 * 1024

	homeDir := getHomeDir()
	s.storagePath = homeDir + outputDirectory
	outputPatternCStr := C.CString(homeDir + outputPattern)
	defer C.free(unsafe.Pointer(outputPatternCStr))

	var fmt_ctx *C.AVFormatContext = nil
	ret := C.avformat_alloc_output_context2(&fmt_ctx, nil, C.CString("segment"), outputPatternCStr)
	if ret < 0 {
		return nil, fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}

	var stream *C.AVStream = nil
	stream = C.avformat_new_stream(fmt_ctx, nil)
	if stream == nil {
		return nil, errors.New("failed to allocate stream")
	}
	stream.id = C.int(fmt_ctx.nb_streams) - 1
	stream.time_base = C.AVRational{num: 1, den: 25} // for 25 FPS

	// copy codec parameters from encoder to segment stream
	codecpar := C.avcodec_parameters_alloc()
	defer C.avcodec_parameters_free(&codecpar)
	if ret := C.avcodec_parameters_from_context(codecpar, enc.codecCtx); ret < 0 {
		return nil, fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
	}
	ret = C.avcodec_parameters_copy(stream.codecpar, codecpar)
	if ret < 0 {
		return nil, fmt.Errorf("failed to copy codec parameters %s", ffmpegError(ret))
	}

	segmentLengthCStr := C.CString(fmt.Sprintf("%d", clipLength))
	segmentFormatCStr := C.CString("mp4")
	resetTimestampsCStr := C.CString("1")
	breakNonKeyFramesCStr := C.CString("1")
	strftimeCStr := C.CString("1")
	defer C.free(unsafe.Pointer(segmentLengthCStr))
	defer C.free(unsafe.Pointer(segmentFormatCStr))
	defer C.free(unsafe.Pointer(resetTimestampsCStr))
	defer C.free(unsafe.Pointer(breakNonKeyFramesCStr))
	defer C.free(unsafe.Pointer(strftimeCStr))

	var opts *C.AVDictionary = nil
	defer C.av_dict_free(&opts)
	ret = C.av_dict_set(&opts, C.CString("segment_time"), segmentLengthCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("failed to set segment_time: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("segment_format"), segmentFormatCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("failed to set segment_format: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("reset_timestamps"), resetTimestampsCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("failed to set reset_timestamps: %s", ffmpegError(ret))
	}
	// TODO(seanp): Allowing this could cause flakey playback. Remove if not needed.
	// Or, fix by adding keyframe forces on the encoder side
	ret = C.av_dict_set(&opts, C.CString("break_non_keyframes"), breakNonKeyFramesCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("failed to set break_non_keyframes: %s", ffmpegError(ret))
	}
	ret = C.av_dict_set(&opts, C.CString("strftime"), strftimeCStr, 0)
	if ret < 0 {
		return nil, fmt.Errorf("failed to set strftime: %s", ffmpegError(ret))
	}

	// segment options must be packed in the initial header write
	ret = C.avformat_write_header(fmt_ctx, &opts)
	if ret < 0 {
		return nil, fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	// Writing header overwrites the time_base, so we need to reset it
	stream.time_base = C.AVRational{num: 1, den: 25}
	stream.id = C.int(fmt_ctx.nb_streams) - 1

	s.stream = stream
	s.outCtx = fmt_ctx

	return s, nil
}

// writeEncodedFrame writes an encoded frame to the output file.
func (s *segmenter) writeEncodedFrame(encodedData []byte, pts int64, dts int64) error {
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

// cleanupStorage cleans up the storage directory.
func (s *segmenter) cleanupStorage() error {
	currStorageSize, err := getDirectorySize(s.storagePath)
	if err != nil {
		return err
	}
	if currStorageSize < s.maxStorageSize {
		return nil
	}
	files, err := getSortedFiles(s.storagePath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if currStorageSize < s.maxStorageSize {
			break
		}
		err := os.Remove(file)
		if err != nil {
			return err
		}
		s.logger.Infof("deleted file: %s", file)
		currStorageSize, err = getDirectorySize(s.storagePath)
		if err != nil {
			return err
		}
	}

	return nil
}

// Close closes the segmenter and writes the trailer.
func (s *segmenter) Close() {
	ret := C.av_write_trailer(s.outCtx)
	if ret < 0 {
		s.logger.Errorf("failed to write trailer", "error", ffmpegError(ret))
	}
	C.avformat_free_context(s.outCtx)
}
