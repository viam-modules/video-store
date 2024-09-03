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
	concatFile  *os.File
}

func newConcater(logger logging.Logger, storagePath string, uploadPath string, camName string) (*concater, error) {
	// create concat file
	// TODO(seanp): figure out where to put this file (some temp dir)
	concatFile, err := os.Create("/home/viam/.viam/" + conactTextFileName)
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
	}, nil
}

// concat takes in from and to timestamps and concates the video files between them.
// returns the path to the concated video file.
func (c *concater) concat(from time.Time, to time.Time, metadata string) (string, error) {
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
	matchingFiles := matchStorageToRange(storageFiles, from, to)
	if len(matchingFiles) == 0 {
		return "", errors.New("no matching video data to save")
	}

	// Clear the concat file and write the matching files list to it.
	c.concatFile.Truncate(0)
	c.concatFile.Seek(0, 0)
	for _, file := range matchingFiles {
		_, err := c.concatFile.WriteString(fmt.Sprintf("file '%s'\n", file))
		if err != nil {
			return "", err
		}
	}

	// open the concat format context
	concatFilePath := C.CString(c.concatFile.Name())
	defer C.free(unsafe.Pointer(concatFilePath))
	concatStr := C.CString("concat")
	defer C.free(unsafe.Pointer(concatStr))
	inputFormat := C.av_find_input_format(concatStr)
	if inputFormat == nil {
		return "", errors.New("failed to find input format")
	}

	// Open the input format context with the concat demuxer.
	// This block sets up the input format context to read the concatenated input files.
	// It uses the concat demuxer with the 'safe' option set to '0' to allow absolute paths in the input file list.
	var options *C.AVDictionary
	safeStr := C.CString("safe")
	safeValStr := C.CString("0")
	defer C.free(unsafe.Pointer(safeValStr))
	defer C.free(unsafe.Pointer(safeStr))
	defer C.av_dict_free(&options)
	C.av_dict_set(&options, safeStr, safeValStr, 0)
	var inputCtx *C.AVFormatContext
	ret := C.avformat_open_input(&inputCtx, concatFilePath, inputFormat, &options)
	if ret < 0 {
		return "", fmt.Errorf("failed to open input format: %s", ffmpegError(ret))
	}
	ret = C.avformat_find_stream_info(inputCtx, nil)
	if ret < 0 {
		return "", fmt.Errorf("failed to find stream info: %s", ffmpegError(ret))
	}

	// create the output format context
	// create output filename
	var outputFilename string
	fromStr := formatDateTimeToString(from)
	toStr := formatDateTimeToString(to)
	if metadata == "" {
		outputFilename = fmt.Sprintf("%s_%s_%s.%s", c.camName, fromStr, toStr, defaultVideoFormat)
	} else {
		outputFilename = fmt.Sprintf("%s_%s_%s_%s.%s", c.camName, fromStr, toStr, metadata, defaultVideoFormat)
	}
	outputPath := filepath.Join(c.uploadPath, outputFilename)
	c.logger.Debug("outputPath", outputPath)
	outputPathCStr := C.CString(outputPath)
	defer C.free(unsafe.Pointer(outputPathCStr))
	var outputCtx *C.AVFormatContext
	ret = C.avformat_alloc_output_context2(&outputCtx, nil, nil, outputPathCStr)
	if ret < 0 {
		return "", fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}

	// Copy codec info from input to output context. This is necessary to ensure
	// we do not decode and re-encode the video data.
	for i := 0; i < int(inputCtx.nb_streams); i++ {
		inStream := *(**C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams)) + uintptr(i)*unsafe.Sizeof(inputCtx.streams)))
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

	// Iterate through each packet in the input context and write it to the output context.
	// TODO(seanp): We can hopefully optimize this by copying input segments entirely instead of packet by packet.
	packet := C.av_packet_alloc()
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
		// Adjust the PTS, DTS, and duration correctly for each packet.
		// Can have multiple streams, so need to adjust each packet based on the stream it belongs to.
		inStream := *(**C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams)) + uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0))))
		outStream := *(**C.AVStream)(unsafe.Pointer(uintptr(unsafe.Pointer(outputCtx.streams)) + uintptr(packet.stream_index)*unsafe.Sizeof(uintptr(0))))
		packet.pts = C.av_rescale_q_rnd(packet.pts, inStream.time_base, outStream.time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)
		packet.dts = C.av_rescale_q_rnd(packet.dts, inStream.time_base, outStream.time_base, C.AV_ROUND_NEAR_INF|C.AV_ROUND_PASS_MINMAX)
		packet.duration = C.av_rescale_q(packet.duration, inStream.time_base, outStream.time_base)
		packet.pos = -1
		ret = C.av_interleaved_write_frame(outputCtx, packet)
		if ret < 0 {
			return "", fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
		}
	}

	ret = C.av_write_trailer(outputCtx)
	if ret < 0 {
		return "", fmt.Errorf("failed to write trailer: %s", ffmpegError(ret))
	}

	C.avio_closep(&outputCtx.pb)
	C.avformat_close_input(&inputCtx)
	C.avformat_free_context(outputCtx)
	C.av_packet_free(&packet)

	return outputPath, nil
}

func (c *concater) close() {
	c.concatFile.Close()
	os.Remove(c.concatFile.Name())
}