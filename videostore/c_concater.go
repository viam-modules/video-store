package videostore

/*
#include "../src/concat.h"
#include <stdlib.h>
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

type c_concater struct {
	logger      logging.Logger
	storagePath string
	uploadPath  string
	segmentDur  time.Duration
}

func newCConcater(
	logger logging.Logger,
	storagePath, uploadPath string,
	segmentSeconds int,
) (*c_concater, error) {
	c := &c_concater{
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
func (c *c_concater) Concat(from, to time.Time, path string) error {
	// Find the storage files that match the concat query.
	storageFiles, err := getSortedFiles(c.storagePath)
	if err != nil {
		c.logger.Error("failed to get sorted files", err)
		return err
	}
	if len(storageFiles) == 0 {
		err := errors.New("no video data in storage")
		c.logger.Errorf("%s, path: %s", err.Error(), path)
		return err
	}
	err = validateTimeRange(storageFiles, from, to)
	if err != nil {
		return err
	}
	matchingFiles := matchStorageToRange(storageFiles, from, to, c.segmentDur)
	if len(matchingFiles) == 0 {
		return errors.New("no matching video data to save")
	}

	// Create a temporary file to store the list of files to concatenate.
	concatFilePath := generateConcatFilePath()
	concatTxtFile, err := os.Create(concatFilePath)
	if err != nil {
		return err
	}
	defer func() {
		if err := concatTxtFile.Close(); err != nil {
			c.logger.Error("failed to close concat file %s, err: %s", concatFilePath, err.Error())
		}

		// Delete tmp concat txt file
		if err := os.Remove(concatFilePath); err != nil {
			c.logger.Error("failed to remove concat file %s, err: %s", concatFilePath, err.Error())
		}
	}()
	for _, file := range matchingFiles {
		_, err := concatTxtFile.WriteString(file + "\n")
		if err != nil {
			return err
		}
	}

	concatFilePathCStr := C.CString(concatFilePath)
	outputPathCStr := C.CString(path)
	defer func() {
		C.free(unsafe.Pointer(concatFilePathCStr))
		C.free(unsafe.Pointer(outputPathCStr))
	}()

	ret := C.video_store_concat(concatFilePathCStr, outputPathCStr)
	switch ret {
	// case C.VIDEO_STORE_CONCAT_RESP_OK:
	case 0:
		return nil
	// case C.VIDEO_STORE_CONCAT_RESP_ERROR:
	case 1:
		return errors.New("failed to concat segment files")
	default:
		return fmt.Errorf("failed to concat segment files: error: %s", ffmpegError(ret))
	}
}

// cleanupConcatTxtFiles cleans up the concat txt files in the tmp directory.
// This is precautionary to ensure that no dangling files are left behind if the
// module is closed during a concat operation.
func (c *c_concater) cleanupConcatTxtFiles() error {
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
