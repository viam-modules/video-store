package filtered_video

/*
#include <libavutil/error.h>
#include <libavutil/opt.h>
#include <libavcodec/avcodec.h>
*/
import "C"
import (
	"encoding/csv"
	"os"
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

// parseCSV reads a CSV file from the provided path and returns a list of a list of strings.
func parseCSV(path string) ([][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	lines, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	return lines, nil
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
