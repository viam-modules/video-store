package videostore

/*
#include <libavcodec/avcodec.h>
#include <libavformat/avformat.h>
#include <libavcodec/avcodec.h>
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"go.viam.com/rdk/logging"
)

const (
	conactTextFileName = "concat.txt"
)

type concater struct {
	logger      logging.Logger
	storagePath string
	uploadPath  string
	camName     string
	segmentDur  time.Duration
	concatFile  *os.File
}

func newConcater(
	logger logging.Logger,
	storagePath, uploadPath, camName string,
	segmentSeconds int,
) (*concater, error) {
	concatPath := filepath.Join(getHomeDir(), ".viam", conactTextFileName)
	logger.Debugf("concatPath: %s", concatPath)
	concatFile, err := os.Create(concatPath)
	if err != nil {
		logger.Error("failed to create concat file", err)
		return nil, err
	}
	return &concater{
		logger:      logger,
		storagePath: storagePath,
		uploadPath:  uploadPath,
		concatFile:  concatFile,
		camName:     camName,
		segmentDur:  time.Duration(segmentSeconds) * time.Second,
	}, nil
}

// concat takes in from and to timestamps and concates the video files between them.
// returns the path to the concated video file.
func (c *concater) concat(from, to time.Time, metadata, path string) (string, error) {
	// Find the storage files that match the concat query.
	storageFiles, err := getSortedFiles(c.storagePath)
	if err != nil {
		c.logger.Error("failed to get sorted files", err)
		return "", err
	}
	if len(storageFiles) == 0 {
		return "", errors.New("no video data in storage")
	}
	err = validateTimeRange(storageFiles, from, to)
	if err != nil {
		return "", err
	}
	matchingFiles := matchStorageToRange(storageFiles, from, to, c.segmentDur)
	if len(matchingFiles) == 0 {
		return "", errors.New("no matching video data to save")
	}

	// Clear the concat file and write the matching files list to it.
	c.concatFile.Truncate(0)
	c.concatFile.Seek(0, 0)
	for _, file := range matchingFiles {
		_, err := c.concatFile.WriteString(file + "\n")
		if err != nil {
			return "", err
		}
	}

	concatFilePath := C.CString(c.concatFile.Name())
	concatStr := C.CString("concat")
	defer func() {
		C.free(unsafe.Pointer(concatFilePath))
		C.free(unsafe.Pointer(concatStr))
	}()
	inputFormat := C.av_find_input_format(concatStr)
	if inputFormat == nil {
		return "", errors.New("failed to find input format")
	}

	// Open the input format context with the concat demuxer. This block sets up
	// the input format context to read the concatenated input files. It uses the
	// concat demuxer with the 'safe' option set to '0' to allow absolute paths in
	// the input file list.
	var options *C.AVDictionary
	safeStr := C.CString("safe")
	safeValStr := C.CString("0")
	var inputCtx *C.AVFormatContext
	defer func() {
		C.free(unsafe.Pointer(safeValStr))
		C.free(unsafe.Pointer(safeStr))
		C.av_dict_free(&options)
		C.avformat_close_input(&inputCtx)
	}()
	ret := C.av_dict_set(&options, safeStr, safeValStr, 0)
	if ret < 0 {
		return "", fmt.Errorf("failed to set option: %s", ffmpegError(ret))
	}
	ret = C.avformat_open_input(&inputCtx, concatFilePath, inputFormat, &options)
	if ret < 0 {
		return "", fmt.Errorf("failed to open input format: %s", ffmpegError(ret))
	}
	ret = C.avformat_find_stream_info(inputCtx, nil)
	if ret < 0 {
		return "", fmt.Errorf("failed to find stream info: %s", ffmpegError(ret))
	}

	// Open the output format context and write the header. This block sets up the
	// output format context to write the concatenated video data to a new file.
	var outputFilename string
	fromStr := formatDateTimeToString(from)
	if metadata == "" {
		outputFilename = fmt.Sprintf("%s_%s.%s", c.camName, fromStr, defaultVideoFormat)
	} else {
		outputFilename = fmt.Sprintf("%s_%s_%s.%s", c.camName, fromStr, metadata, defaultVideoFormat)
	}
	outputPath := filepath.Join(path, outputFilename)
	outputPathCStr := C.CString(outputPath)
	var outputCtx *C.AVFormatContext
	defer func() {
		C.free(unsafe.Pointer(outputPathCStr))
		C.avio_closep(&outputCtx.pb)
		C.avformat_free_context(outputCtx)
	}()

	ret = C.avformat_alloc_output_context2(&outputCtx, nil, nil, outputPathCStr)
	if ret < 0 {
		return "", fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}

	// Copy codec info from input to output context. This is necessary to ensure
	// we do not decode and re-encode the video data.
	for i := 0; i < int(inputCtx.nb_streams); i++ {
		inStream := *(**C.AVStream)(
			unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams)) +
				uintptr(i)*unsafe.Sizeof(inputCtx.streams)))
		outStream := C.avformat_new_stream(outputCtx, nil)
		if outStream == nil {
			return "", fmt.Errorf("failed to allocate stream")
		}
		ret := C.avcodec_parameters_copy(outStream.codecpar, inStream.codecpar)
		if ret < 0 {
			return "", fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
		}
		// Let ffmpeg handle the codec tag for us.
		outStream.codecpar.codec_tag = 0
	}

	// Open the output file and write the header.
	ret = C.avio_open(&outputCtx.pb, outputPathCStr, C.AVIO_FLAG_WRITE)
	if ret < 0 {
		return "", fmt.Errorf("failed to open output file: %s", ffmpegError(ret))
	}
	ret = C.avformat_write_header(outputCtx, nil)
	if ret < 0 {
		return "", fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	// Adjust the PTS, DTS, and duration correctly for each packet.
	// TODO(seanp): We can hopefully optimize this by copying input segments entirely
	// instead of packet by packet.
	packet := C.av_packet_alloc()
	defer C.av_packet_free(&packet)
	for {
		ret := C.av_read_frame(inputCtx, packet)
		if ret == C.AVERROR_EOF {
			c.logger.Debug("Concatenation complete. Hit EOF.")
			break
		}
		// Any error other than EOF is a problem.
		if ret < 0 {
			return "", fmt.Errorf("failed to read frame: %s", ffmpegError(ret))
		}
		// Can have multiple streams, so need to adjust each packet based on the
		// stream it belongs to.
		inputStreamsBase := unsafe.Pointer(inputCtx.streams)
		inputStreamOffset := uintptr(packet.stream_index) * unsafe.Sizeof(inputCtx.streams)
		inStream := *(**C.AVStream)(unsafe.Pointer(uintptr(inputStreamsBase) + inputStreamOffset))
		outputStreamsBase := unsafe.Pointer(outputCtx.streams)
		outputStreamOffset := uintptr(packet.stream_index) * unsafe.Sizeof(outputCtx.streams)
		outStream := *(**C.AVStream)(unsafe.Pointer(uintptr(outputStreamsBase) + outputStreamOffset))

		packet.pts = C.av_rescale_q_rnd(packet.pts, inStream.time_base, outStream.time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)
		packet.dts = C.av_rescale_q_rnd(packet.dts, inStream.time_base, outStream.time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)
		packet.duration = C.av_rescale_q(packet.duration, inStream.time_base, outStream.time_base)
		packet.pos = -1
		ret = C.av_interleaved_write_frame(outputCtx, packet)
		if ret < 0 {
			return "", fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
		}
	}

	// Write the trailer, close the output file, and free context memory.
	ret = C.av_write_trailer(outputCtx)
	if ret < 0 {
		return "", fmt.Errorf("failed to write trailer: %s", ffmpegError(ret))
	}

	return outputPath, nil
}

// close closes the concater and removes the concat file.
// Do not need to clean up FFmpeg resources as they are handled in the concat function.
func (c *concater) close() {
	if err := c.concatFile.Close(); err != nil {
		c.logger.Error("failed to close concat file", err)
	}
	if err := os.Remove(c.concatFile.Name()); err != nil {
		c.logger.Error("failed to remove concat file", err)
	}
}
