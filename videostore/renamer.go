package videostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.viam.com/rdk/logging"
)

// renamer watches a directory and converts local timestamps to unix timestamps
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
func (r *renamer) processSegments(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	r.logger.Debugf("Starting to scan directory: %s", r.watchDir)
	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("Context done, stopping scanner and flushing remaining files")
			if err := r.scanAndProcessFiles(); err != nil {
				r.logger.Debugf("Error scanning directory: %v", err)
			}
			return nil
		case <-ticker.C:
			if err := r.scanAndProcessFiles(); err != nil {
				r.logger.Debugf("Error scanning directory: %v", err)
			}
		}
	}
}

// scanAndProcessFiles scans the watch directory for MP4 files and processes them
func (r *renamer) scanAndProcessFiles() error {
	files, err := r.getMPEGFiles()
	if err != nil {
		return err
	}

	return r.processFiles(files)

}

// getMPEGFiles retrieves valid and complete MP4 files in the watch directory
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
		_, err := getVideoInfo(fullPath)
		if err == nil {
			mpegFiles = append(mpegFiles, fullPath)
		} else {
			r.logger.Debugf("Not a valid video file, skipping %s: %v", fullPath, err)
		}
	}

	return mpegFiles, nil
}

// processFiles renames a list of files
func (r *renamer) processFiles(files []string) error {
	if len(files) == 0 {
		return errors.New("no files provided for processing")
	}
	var lastErr error
	for _, file := range files {
		r.logger.Debugf("Processing file: %s", file)
		if err := r.convertFilenameToUnixTimestamp(file); err != nil {
			r.logger.Errorf("Failed to process %s: %v", file, err)
			lastErr = err
		}
	}

	return lastErr
}

// convertFilenameToUnixTimestamp changes filename from local datetime to unix timestamp
func (r *renamer) convertFilenameToUnixTimestamp(filePath string) error {
	filename := filepath.Base(filePath)
	timestampStr := strings.TrimSuffix(filename, ".mp4")
	localTime, err := ParseDateTimeString(timestampStr)
	if err != nil {
		return fmt.Errorf("failed to parse timestamp from filename %s: %w", filename, err)
	}
	unixTimestamp := localTime.Unix()
	unixFilename := fmt.Sprintf("%d.mp4", unixTimestamp)
	outputPath := filepath.Join(r.outputDir, unixFilename)
	r.logger.Debugf("converting %s to UTC: %s", filename, unixFilename)
	if err := os.Rename(filePath, outputPath); err != nil {
		return fmt.Errorf("failed to rename file %s to %s: %w", filePath, outputPath, err)
	}

	return nil
}
