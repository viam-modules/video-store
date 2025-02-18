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

type rtpconcater struct {
	logger      logging.Logger
	storagePath string
	uploadPath  string
	segmentDur  time.Duration
}

func newRTPConcater(
	logger logging.Logger,
	storagePath, uploadPath string,
	segmentSeconds int,
) (*rtpconcater, error) {
	c := &rtpconcater{
		logger:      logger,
		storagePath: storagePath,
		uploadPath:  uploadPath,
		segmentDur:  time.Duration(segmentSeconds) * time.Second,
	}
	err := c.cleanupConcatTxtFiles()
	if err != nil {
		c.logger.Error("failed to cleanup concat txt files", err)
	}
	return c, nil
}

// concat takes in from and to timestamps and concates the video files between them.
// returns the path to the concated video file.
func (c *rtpconcater) Concat(from, to time.Time, path string) error {
	c.logger.Info("Concat START")
	defer c.logger.Info("Concat END")
	// Find the storage files that match the concat query.
	storageFiles, err := getSortedFiles(c.storagePath)
	if err != nil {
		c.logger.Error("failed to get sorted files", err)
		return err
	}
	if len(storageFiles) == 0 {
		return errors.New("no video data in storage")
	}
	err = validateTimeRange(storageFiles, from, to)
	if err != nil {
		return err
	}
	matchingFiles := matchStorageToRange(storageFiles, from, to, c.segmentDur)
	if len(matchingFiles) == 0 {
		return errors.New("no matching video data to save")
	}

	c.logger.Infof("matching files: %#v", matchingFiles)

	// Create a temporary file to store the list of files to concatenate.
	concatFilePath := generateConcatFilePath()
	concatTxtFile, err := os.Create(concatFilePath)
	if err != nil {
		return err
	}

	defer concatTxtFile.Close()
	for _, file := range matchingFiles {
		_, err := concatTxtFile.WriteString(file + "\n")
		if err != nil {
			return err
		}
	}
	c.logger.Infof("concatFilePath: %#v", concatFilePath)
	concatFileContents, err := os.ReadFile(concatFilePath)
	if err != nil {
		return err
	}
	c.logger.Infof("concatFileContents: %s", string(concatFileContents))

	concatFilePathCStr := C.CString(concatFilePath)
	concatCStr := C.CString("concat")
	defer func() {
		C.free(unsafe.Pointer(concatFilePathCStr))
		C.free(unsafe.Pointer(concatCStr))
	}()
	inputFormat := C.av_find_input_format(concatCStr)
	if inputFormat == nil {
		return errors.New("failed to find input format")
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
		return fmt.Errorf("failed to set option: %s", ffmpegError(ret))
	}
	ret = C.avformat_open_input(&inputCtx, concatFilePathCStr, inputFormat, &options)
	if ret < 0 {
		return fmt.Errorf("failed to open input format: %s", ffmpegError(ret))
	}
	ret = C.avformat_find_stream_info(inputCtx, nil)
	if ret < 0 {
		return fmt.Errorf("failed to find stream info: %s", ffmpegError(ret))
	}

	// Open the output format context and write the header. This block sets up the
	// output format context to write the concatenated video data to a new file.
	outputPathCStr := C.CString(path)
	var outputCtx *C.AVFormatContext
	defer func() {
		C.free(unsafe.Pointer(outputPathCStr))
		C.avio_closep(&outputCtx.pb)
		C.avformat_free_context(outputCtx)
	}()

	ret = C.avformat_alloc_output_context2(&outputCtx, nil, nil, outputPathCStr)
	if ret < 0 {
		return fmt.Errorf("failed to allocate output context: %s", ffmpegError(ret))
	}

	c.logger.Infof("streams len: %d", int(inputCtx.nb_streams))
	// Copy codec info from input to output context. This is necessary to ensure
	// we do not decode and re-encode the video data.
	for i := range int(inputCtx.nb_streams) {
		inStream := *(**C.AVStream)(
			unsafe.Pointer(uintptr(unsafe.Pointer(inputCtx.streams)) +
				uintptr(i)*unsafe.Sizeof(inputCtx.streams)))
		outStream := C.avformat_new_stream(outputCtx, nil)
		if outStream == nil {
			return errors.New("failed to allocate stream")
		}
		ret := C.avcodec_parameters_copy(outStream.codecpar, inStream.codecpar)
		if ret < 0 {
			return fmt.Errorf("failed to copy codec parameters: %s", ffmpegError(ret))
		}
	}

	// Open the output file and write the header.
	ret = C.avio_open(&outputCtx.pb, outputPathCStr, C.AVIO_FLAG_WRITE)
	if ret < 0 {
		return fmt.Errorf("failed to open output file: %s", ffmpegError(ret))
	}
	ret = C.avformat_write_header(outputCtx, nil)
	if ret < 0 {
		return fmt.Errorf("failed to write header: %s", ffmpegError(ret))
	}

	// Adjust the PTS, DTS, and duration correctly for each packet.
	// TODO(seanp): We can hopefully optimize this by copying input segments entirely
	// instead of packet by packet.
	packet := C.av_packet_alloc()
	defer C.av_packet_free(&packet)
	i := 0
	for {
		c.logger.Infof("iteration: %d", i)
		i++
		ret := C.av_read_frame(inputCtx, packet)
		if ret == C.AVERROR_EOF {
			c.logger.Debug("Concatenation complete. Hit EOF.")
			break
		}
		// Any error other than EOF is a problem.
		if ret < 0 {
			return fmt.Errorf("failed to read frame: %s", ffmpegError(ret))
		}
		c.logger.Infof("av_read_frame ret: %d", int(ret))
		c.logger.Infof("pts: %d, dts: %d, pos: %d, duration: %d, stream_index: %d, time_base: %d, packet.flags: %d",
			packet.pts, packet.dts, packet.pos, packet.duration, packet.stream_index, packet.time_base, packet.flags)
		if int(packet.flags)&(C.AV_PKT_FLAG_DISCARD) == (C.AV_PKT_FLAG_DISCARD) {
			c.logger.Info("packet is to be discarded")
			continue
		}
		ret = C.av_interleaved_write_frame(outputCtx, packet)
		if ret < 0 {
			err := fmt.Errorf("failed to write frame: %s", ffmpegError(ret))
			c.logger.Error(err.Error())
			return err
		}
	}

	// Write the trailer, close the output file, and free context memory.
	ret = C.av_write_trailer(outputCtx)
	if ret < 0 {
		return fmt.Errorf("failed to write trailer: %s", ffmpegError(ret))
	}

	// Delete tmp concat txt file
	if err := os.Remove(concatFilePath); err != nil {
		c.logger.Error("failed to remove concat file", err)
	}

	return nil
}

// cleanupConcatTxtFiles cleans up the concat txt files in the tmp directory.
// This is precautionary to ensure that no dangling files are left behind if the
// module is closed during a concat operation.
func (c *rtpconcater) cleanupConcatTxtFiles() error {
	pattern := fmt.Sprintf(conactTxtFilePattern, "*")
	files, err := filepath.Glob(filepath.Join(concatTxtDir, pattern))
	if err != nil {
		c.logger.Error("failed to list files in /tmp", err)
		return err
	}
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			c.logger.Error("failed to remove file", err)
		}
	}
	return nil
}
