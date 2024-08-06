package filtered_video

/*
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <libavcodec/avcodec.h>
*/
import "C"
import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func ffmpegError(ret C.int) string {
	var errbuf [256]C.char
	C.av_strerror(ret, &errbuf[0], 256)
	if errbuf[0] == 0 {
		return "unknown error"
	}
	return C.GoString(&errbuf[0])
}

func ffmppegLogLevel(loglevel C.int) {
	// TODO(seanp): make sure log level is valid before setting
	C.av_log_set_level(loglevel)
}

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

func lookupCodecID(codec string) C.enum_AVCodecID {
	switch codec {
	case "h264":
		return C.AV_CODEC_ID_H264
	default:
		return C.AV_CODEC_ID_NONE
	}
}

func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func now() int64 {
	return time.Now().UnixNano() / int64(time.Millisecond)
}

// combineClips reads mp4 files from the provided list of files and combines them into a single mp4 file.
func combineClips(files []string, output string) error {
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
	return size, err
}

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
		timeI, errI := extractDateTime(filePaths[i])
		timeJ, errJ := extractDateTime(filePaths[j])
		if errI != nil || errJ != nil {
			return false
		}
		return timeI.Before(timeJ)
	})

	return filePaths, nil
}

func extractDateTime(filePath string) (time.Time, error) {
	baseName := filepath.Base(filePath)
	parts := strings.Split(baseName, "_")
	if len(parts) < 3 {
		return time.Time{}, fmt.Errorf("invalid file name: %s", baseName)
	}
	datePart := parts[1]
	timePart := strings.TrimSuffix(parts[2], filepath.Ext(baseName))
	dateTimeStr := datePart + "_" + timePart
	dateTime, err := time.Parse("2006-01-02_15-04-05", dateTimeStr)
	if err != nil {
		return time.Time{}, err
	}
	return dateTime, nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	if err != nil {
		return err
	}

	err = destinationFile.Sync()
	if err != nil {
		return err
	}

	return nil
}
