package videostore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
// and processing them in order of creation.
func (r *renamer) processSegments(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	r.logger.Debugf("Starting to scan directory: %s", r.watchDir)
	for {
		select {
		case <-ctx.Done():
			r.logger.Debug("Context done, stopping scanner and flushing remaining files")
			if err := r.close(); err != nil {
				return fmt.Errorf("error during close: %w", err)
			}
			return nil
		case <-ticker.C:
			if err := r.scanAndProcessFiles(); err != nil {
				r.logger.Errorf("Error scanning directory: %v", err)
			}
		}
	}
}

// scanAndProcessFiles scans the watch directory for MP4 files and processes them
//
// It excludes the most recent file to avoid processing it while it's still
// being written by the segmenter.
func (r *renamer) scanAndProcessFiles() error {
	files, err := r.getSortedMPEGFiles()
	if err != nil {
		return err
	}
	if len(files) <= 1 {
		r.logger.Debug("one or fewer active files found, skipping processing")
		return nil
	}

	return r.processFiles(files[:len(files)-1]) // Exclude the most recent file
}

// getSortedMPEGFiles retrieves and sorts MP4 files in the watch directory
func (r *renamer) getSortedMPEGFiles() ([]string, error) {
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
		if strings.HasSuffix(name, ".mp4") {
			fullPath := filepath.Join(r.watchDir, name)
			mpegFiles = append(mpegFiles, fullPath)
		}
	}
	// Sort files by modification time (oldest first)
	sort.Slice(mpegFiles, func(i, j int) bool {
		infoI, _ := os.Stat(mpegFiles[i])
		infoJ, _ := os.Stat(mpegFiles[j])
		return infoI.ModTime().Before(infoJ.ModTime())
	})

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

// convertFilenameToUnixTimestamp converts a filename to unix timestamp format
//
// Converting to unix timestamp is only necessary on Windows systems where the
// local time is used in the filename. FFmpeg is unable to use strftime to format
// filenames in unix timestamp format, so we need to convert using golang here.
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

// close flushes any remaining files in the queue to disk
func (r *renamer) close() error {
	r.logger.Debug("Closing renamer, processing ALL remaining files")
	files, err := r.getSortedMPEGFiles()
	if err != nil {
		return fmt.Errorf("failed to get files during close: %w", err)
	}
	if err := r.processFiles(files); err != nil {
		return fmt.Errorf("errors occurred while flushing remaining files: %w", err)
	}

	return nil
}
