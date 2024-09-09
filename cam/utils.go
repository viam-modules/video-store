package videostore

/*
#include <libavutil/error.h>
#include <libavutil/opt.h>
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
	"sort"
	"strings"
	"time"
)

type codecType int

const (
	// CodecUnknown represents an unknown codec type.
	codecUnknown codecType = iota
	// CodecH264 represents the H.264 codec type.
	codecH264
	// CodecH265 represents the H.265 codec type.
	codecH265
)

func (c codecType) String() string {
	switch c {
	case codecH264:
		return "h264"
	case codecH265:
		return "h265"
	default:
		return "unknown"
	}
}

func parseCodecType(codec string) codecType {
	switch codec {
	case "h264":
		return codecH264
	case "h265":
		return codecH265
	default:
		return codecUnknown
	}
}

// lookupCodecIDByType returns the FFmpeg codec ID for the provided codec type.
func lookupCodecIDByType(codec codecType) C.enum_AVCodecID {
	switch codec {
	case codecH264:
		return C.AV_CODEC_ID_H264
	case codecH265:
		return C.AV_CODEC_ID_H265
	default:
		return C.AV_CODEC_ID_NONE
	}
}

// lookupCodecID returns the codec ID for the provided codec.
func lookupCodecTypeByID(codecID C.enum_AVCodecID) codecType {
	switch codecID {
	case C.AV_CODEC_ID_H264:
		return codecH264
	case C.AV_CODEC_ID_H265:
		return codecH265
	default:
		return codecUnknown
	}
}

// ffmpegError returns a string representation of the ffmpeg error code.
func ffmpegError(ret C.int) string {
	var errbuf [256]C.char
	C.av_strerror(ret, &errbuf[0], 256)
	if errbuf[0] == 0 {
		return "unknown ffmpeg error"
	}
	return C.GoString(&errbuf[0])
}

// ffmppegLogLevel sets the log level for ffmpeg logger.
func ffmppegLogLevel(loglevel C.int) {
	C.av_log_set_level(loglevel)
}

// lookupLogID returns the log ID for the provided log level.
func lookupLogID(level string) C.int {
	switch level {
	case "error":
		return C.AV_LOG_ERROR
	case "warning":
		return C.AV_LOG_WARNING
	case "info":
		return C.AV_LOG_INFO
	case "debug":
		return C.AV_LOG_DEBUG
	default:
		return C.AV_LOG_INFO
	}
}

// getHomeDir returns the home directory of the user.
func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// createDir creates a directory at the provided path if it does not exist.
func createDir(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		err := os.MkdirAll(path, 0o755)
		if err != nil {
			return err
		}
	}
	return nil
}

// readVideoFile takes in a path to mp4 file and returns bytes of the file.
func readVideoFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// getDirectorySize returns the size of a directory in bytes.
func getDirectorySize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return size, err
}

// getFileSize returns the size of a file in bytes.
func getFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// getSortedFiles returns a list of files in the provided directory sorted by creation time.
func getSortedFiles(path string) ([]string, error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var filePaths []string
	for _, file := range files {
		filePaths = append(filePaths, filepath.Join(path, file.Name()))
	}
	sort.Slice(filePaths, func(i, j int) bool {
		timeI, errI := extractDateTimeFromFilename(filePaths[i])
		timeJ, errJ := extractDateTimeFromFilename(filePaths[j])
		if errI != nil || errJ != nil {
			return false
		}
		return timeI.Before(timeJ)
	})

	return filePaths, nil
}

// extractDateTimeFromFilename extracts the date and time from the filename.
func extractDateTimeFromFilename(filePath string) (time.Time, error) {
	baseName := filepath.Base(filePath)
	parts := strings.Split(baseName, "_")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid file name: %s", baseName)
	}
	datePart := parts[0]
	timePart := strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
	dateTimeStr := datePart + "_" + timePart
	return parseDateTimeString(dateTimeStr)
}

// parseDateTimeString parses a date and time string in the format "2006-01-02_15-04-05".
// Returns a time.Time object and an error if the string is not in the correct format.
func parseDateTimeString(datetime string) (time.Time, error) {
	dateTime, err := time.Parse("2006-01-02_15-04-05", datetime)
	if err != nil {
		return time.Time{}, err
	}
	return dateTime, nil
}

func formatDateTimeToString(dateTime time.Time) string {
	return dateTime.Format("2006-01-02_15-04-05")
}

// matchStorageToRange returns a list of files that fall within the provided time range.
// Includes trimming video files to the time range if they overlap. Assumes that all video
// files have the same duration.
func matchStorageToRange(files []string, start, end time.Time, duration time.Duration) []string {
	var matchedFiles []string
	for _, file := range files {
		dateTime, err := extractDateTimeFromFilename(file)
		if err != nil {
			continue
		}
		fileEndTime := dateTime.Add(duration)
		// Check if the file's time range intersects with [start, end)
		if dateTime.Before(end) && fileEndTime.After(start) {
			var inpoint, outpoint float64
			inpointSet := false
			outpointSet := false
			// Calculate inpoint if the file starts before the 'start' time and overlaps
			if dateTime.Before(start) {
				inpoint = start.Sub(dateTime).Seconds()
				inpointSet = true
			}
			// Calculate outpoint if the file ends after the 'end' time
			if fileEndTime.After(end) {
				outpoint = end.Sub(dateTime).Seconds()
				outpointSet = true
			}
			matchedFiles = append(matchedFiles, fmt.Sprintf("file '%s'", file))
			if inpointSet {
				matchedFiles = append(matchedFiles, fmt.Sprintf("inpoint %.2f", inpoint))
			}
			if outpointSet && outpoint < duration.Seconds() {
				// Only include outpoint if it's less than the full duration
				matchedFiles = append(matchedFiles, fmt.Sprintf("outpoint %.2f", outpoint))
			}
		}
	}

	return matchedFiles
}

// validateTimeRange validates the start and end time range against storage files.
// Extracts the start timestamp of the oldest file and the start of the most recent file.
// Since the most recent segment file is still being written to by the segmenter
// we do not want to include it in the time range.
func validateTimeRange(files []string, start, end time.Time) error {
	if len(files) == 0 {
		return errors.New("no storage files found")
	}
	oldestFileStart, err := extractDateTimeFromFilename(files[0])
	if err != nil {
		return err
	}
	newestFileStart, err := extractDateTimeFromFilename(files[len(files)-1])
	if err != nil {
		return err
	}
	if start.Before(oldestFileStart) || end.After(newestFileStart) {
		return errors.New("time range is outside of storage range")
	}
	return nil
}

// validateSaveCommand validates the save command params and checks for valid time format.
func validateSaveCommand(command map[string]interface{}) (time.Time, time.Time, string, error) {
	fromStr, ok := command["from"].(string)
	if !ok {
		return time.Time{}, time.Time{}, "", errors.New("from timestamp not found")
	}
	from, err := parseDateTimeString(fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	toStr, ok := command["to"].(string)
	if !ok {
		return time.Time{}, time.Time{}, "", errors.New("to timestamp not found")
	}
	to, err := parseDateTimeString(toStr)
	if err != nil {
		return time.Time{}, time.Time{}, "", err
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, "", errors.New("from timestamp is after to timestamp")
	}
	metadata, ok := command["metadata"].(string)
	if !ok {
		metadata = ""
	}
	return from, to, metadata, nil
}

// validateFetchCommand validates the fetch command params and checks for valid time format.
func validateFetchCommand(command map[string]interface{}) (time.Time, time.Time, error) {
	fromStr, ok := command["from"].(string)
	if !ok {
		return time.Time{}, time.Time{}, errors.New("from timestamp not found")
	}
	from, err := parseDateTimeString(fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	toStr, ok := command["to"].(string)
	if !ok {
		return time.Time{}, time.Time{}, errors.New("to timestamp not found")
	}
	to, err := parseDateTimeString(toStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if from.After(to) {
		return time.Time{}, time.Time{}, errors.New("from timestamp is after to timestamp")
	}
	return from, to, nil
}
