package videostore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	vsutils "github.com/viam-modules/video-store/videostore/utils"
	"go.viam.com/rdk/logging"
)

// renamer watches a directory and converts local timestamps to unix timestamps.
type renamer struct {
	watchDir  string
	outputDir string
	logger    logging.Logger
}

func newRenamer(watchDir, outputDir string, logger logging.Logger) *renamer {
	return &renamer{
		watchDir:  watchDir,
		outputDir: outputDir,
		logger:    logger,
	}
}

// processSegments periodically scans the directory for new files to process
//
// This function polls the directory regularly, looking for MP4 files
// and processing them to convert their filenames from local datetime
// to unix timestamps. It runs until the context is done, at which point
// it flushes any remaining files and exits gracefully.
func (r *renamer) processSegments(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	r.logger.Debugf("Starting to scan directory: %s", r.watchDir)
	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("Context done, stopping scanner and flushing remaining files")
			r.scanAndProcessFiles()
			return
		case <-ticker.C:
			r.scanAndProcessFiles()
		}
	}
}

// scanAndProcessFiles scans the watch directory for MP4 files and processes them.
func (r *renamer) scanAndProcessFiles() {
	files, err := r.getMPEGFiles()
	if err != nil {
		r.logger.Errorf("Failed to get MPEG files: %v", err)
		return
	}
	r.processFiles(files)
}

// getMPEGFiles retrieves valid and complete MP4 files in the watch directory.
func (r *renamer) getMPEGFiles() ([]string, error) {
	entries, err := os.ReadDir(r.watchDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}
	var mpegFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			r.logger.Debugf("Skipping directory entry: %s", entry.Name())
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".mp4") {
			r.logger.Debugf("Skipping non-MP4 file: %s", name)
			continue
		}
		fullPath := filepath.Join(r.watchDir, name)
		// Check if the video segment is completed with MOOV atom present
		_, err := vsutils.GetVideoInfo(fullPath)
		if err == nil {
			mpegFiles = append(mpegFiles, fullPath)
		} else {
			r.logger.Debugf("Not a valid video file, skipping %s: %v", fullPath, err)
		}
	}

	return mpegFiles, nil
}

// processFiles renames a list of files.
func (r *renamer) processFiles(files []string) {
	if len(files) == 0 {
		r.logger.Debug("No files to process")
		return
	}
	for _, file := range files {
		r.logger.Debugf("Processing file: %s", file)
		// if err := r.convertFilenameToUnixTimestamp(file); err != nil {
		unixFilename, err := r.convertFilenameToUnixTimestamp(file)
		if err != nil {
			r.logger.Debugf("Failed to convert %s: %v", file, err)
			continue
		}

		r.logger.Debugf("converting %s to UTC: %s", file, unixFilename)
		if err := os.Rename(file, unixFilename); err != nil {
			r.logger.Errorf("Failed to rename file %s to %s: %v", file, unixFilename, err)
		}
	}
}

// convertFilenameToUnixTimestamp changes filename from local datetime to unix timestamp.
func (r *renamer) convertFilenameToUnixTimestamp(filePath string) (string, error) {
	filename := filepath.Base(filePath)
	timestampStr := strings.TrimSuffix(filename, ".mp4")
	localTime, err := vsutils.ParseDateTimeString(timestampStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse timestamp from filename %s: %w", filename, err)
	}
	unixTimestamp := localTime.Unix()
	unixFilename := fmt.Sprintf("%d.mp4", unixTimestamp)
	outputPath := filepath.Join(r.outputDir, unixFilename)

	return outputPath, nil
}
