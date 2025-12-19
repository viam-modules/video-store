package videostore

/*
#include "concat.h"
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

	"github.com/google/uuid"
	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/logging"
)

// ContainerFormat specifies the output container format for concatenation.
type ContainerFormat int

const (
	// ContainerDefault uses standard MP4 with faststart.
	ContainerDefault ContainerFormat = 0
	// ContainerMP4 uses standard MP4 with faststart (moov at beginning).
	ContainerMP4 ContainerFormat = 1
	// ContainerFMP4 uses fragmented MP4 for streaming.
	ContainerFMP4 ContainerFormat = 2
)

const (
	conactTxtFilePattern = "concat_%s.txt"
)

var concatTxtDir = os.TempDir()

type concater struct {
	logger      logging.Logger
	storagePath string
	uploadPath  string
	segmentDur  time.Duration
}

func newConcater(
	storagePath, uploadPath string,
	logger logging.Logger,
) (*concater, error) {
	c := &concater{
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

// Concat takes in from and to timestamps and concatenates the video files between them.
// Uses ContainerDefault (MP4 with faststart) for the output format.
func (c *concater) Concat(from, to time.Time, path string) error {
	return c.ConcatWithFormat(from, to, path, ContainerDefault)
}

// ConcatWithFormat takes in from and to timestamps and concatenates the video files between them
// using the specified container format.
func (c *concater) ConcatWithFormat(from, to time.Time, path string, container ContainerFormat) error {
	// Find the storage files that match the concat query.
	storageFiles, err := vsutils.GetSortedFiles(c.storagePath)
	if err != nil {
		c.logger.Error("failed to get sorted files", err)
		return err
	}
	if len(storageFiles) == 0 {
		err := errors.New("no video data in storage")
		c.logger.Errorf("%s, path: %s", err.Error(), path)
		return err
	}
	err = vsutils.ValidateTimeRange(storageFiles, from)
	if err != nil {
		return err
	}
	concatEntries := vsutils.MatchStorageToRange(storageFiles, from, to, c.logger)
	if len(concatEntries) == 0 {
		return errors.New("no matching video data to save")
	}

	// Create a temporary file to store the list of files to concatenate.
	concatFilePath := generateConcatFilePath()
	err = writeConcatFileEntries(concatEntries, concatFilePath)
	defer func() {
		// Remove the concat file after the concat operation is complete.
		if _, err := os.Stat(concatFilePath); err == nil {
			if err := os.Remove(concatFilePath); err != nil {
				c.logger.Error("failed to remove concat file %s, err: %s", concatFilePath, err.Error())
			}
		}
	}()
	if err != nil {
		return err
	}

	concatFilePathCStr := C.CString(concatFilePath)
	outputPathCStr := C.CString(path)
	defer func() {
		C.free(unsafe.Pointer(concatFilePathCStr))
		C.free(unsafe.Pointer(outputPathCStr))
	}()

	ret := C.video_store_concat(concatFilePathCStr, outputPathCStr, C.ContainerFormat(container))
	switch ret {
	case C.VIDEO_STORE_CONCAT_RESP_OK:
		return nil
	case C.VIDEO_STORE_CONCAT_RESP_ERROR:
		return errors.New("failed to concat segment files")
	default:
		return fmt.Errorf("failed to concat segment files: error: %s", vsutils.FFmpegError(int(ret)))
	}
}

// writeConcatFileEntries writes the concat file entries to a file.
func writeConcatFileEntries(entries []vsutils.ConcatFileEntry, filePath string) error {
	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, entry := range entries {
		lines := entry.Lines()
		for _, line := range lines {
			_, err := file.WriteString(line + "\n")
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// cleanupConcatTxtFiles cleans up the concat txt files in the tmp directory.
// This is precautionary to ensure that no dangling files are left behind if the
// module is closed during a concat operation.
func (c *concater) cleanupConcatTxtFiles() error {
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

// generateConcatFilePath generates a unique file name for concat txt reference file.
// This allows multiple concats to be run concurrently without conflicts.
func generateConcatFilePath() string {
	uniqueID := uuid.New().String()
	fileName := fmt.Sprintf(conactTxtFilePattern, uniqueID)
	filePath := filepath.Join(concatTxtDir, fileName)
	return filePath
}
