package videostore

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.viam.com/rdk/logging"
	"go.viam.com/test"
)

// findAtomOffset finds the byte offset of an MP4 atom in a file.
// Returns -1 if not found.
func findAtomOffset(data []byte, atomName string) int64 {
	atom := []byte(atomName)
	// MP4 atoms have a 4-byte size followed by 4-byte name
	for i := range len(data) - 8 {
		if bytes.Equal(data[i+4:i+8], atom) {
			return int64(i)
		}
	}
	return -1
}

func TestConcatFaststart(t *testing.T) {
	logger := logging.NewTestLogger(t)
	outputDir := t.TempDir()

	// Get absolute path to artifact storage
	storagePath := getArtifactStoragePath(t)

	c, err := newConcater(storagePath, outputDir, logger)
	test.That(t, err, test.ShouldBeNil)

	// Use known timestamps from artifact files (1725634803.mp4, 1725634833.mp4)
	from := time.Unix(1725634803, 0)
	to := time.Unix(1725634850, 0)

	outputPath := filepath.Join(outputDir, "faststart_test.mp4")
	err = c.Concat(from, to, outputPath)
	test.That(t, err, test.ShouldBeNil)

	data, err := os.ReadFile(outputPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(data), test.ShouldBeGreaterThan, 0)

	moovOffset := findAtomOffset(data, "moov")
	mdatOffset := findAtomOffset(data, "mdat")

	t.Logf("moov atom offset: %d", moovOffset)
	t.Logf("mdat atom offset: %d", mdatOffset)

	// With faststart, moov should come before mdat
	test.That(t, moovOffset, test.ShouldBeGreaterThan, -1)
	test.That(t, mdatOffset, test.ShouldBeGreaterThan, -1)
	test.That(t, moovOffset, test.ShouldBeLessThan, mdatOffset)
}

func TestConcatMP4(t *testing.T) {
	logger := logging.NewTestLogger(t)
	outputDir := t.TempDir()
	storagePath := getArtifactStoragePath(t)

	c, err := newConcater(storagePath, outputDir, logger)
	test.That(t, err, test.ShouldBeNil)

	from := time.Unix(1725634803, 0)
	to := time.Unix(1725634850, 0)

	outputPath := filepath.Join(outputDir, "mp4_test.mp4")
	err = c.ConcatWithFormat(from, to, outputPath, ContainerMP4)
	test.That(t, err, test.ShouldBeNil)

	data, err := os.ReadFile(outputPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(data), test.ShouldBeGreaterThan, 0)

	// Verify ftyp atom exists (MP4 file signature)
	ftypOffset := findAtomOffset(data, "ftyp")
	test.That(t, ftypOffset, test.ShouldEqual, 0)

	moovOffset := findAtomOffset(data, "moov")
	mdatOffset := findAtomOffset(data, "mdat")

	t.Logf("ftyp offset: %d, moov offset: %d, mdat offset: %d", ftypOffset, moovOffset, mdatOffset)

	// With faststart (ContainerMP4), moov should come before mdat
	// Expected layout: [ftyp][moov][mdat]
	test.That(t, moovOffset, test.ShouldBeGreaterThan, -1)
	test.That(t, mdatOffset, test.ShouldBeGreaterThan, -1)
	test.That(t, moovOffset, test.ShouldBeLessThan, mdatOffset)
}

func TestConcatFMP4(t *testing.T) {
	logger := logging.NewTestLogger(t)
	outputDir := t.TempDir()
	storagePath := getArtifactStoragePath(t)

	c, err := newConcater(storagePath, outputDir, logger)
	test.That(t, err, test.ShouldBeNil)

	from := time.Unix(1725634803, 0)
	to := time.Unix(1725634850, 0)

	outputPath := filepath.Join(outputDir, "fmp4_test.mp4")
	err = c.ConcatWithFormat(from, to, outputPath, ContainerFMP4)
	test.That(t, err, test.ShouldBeNil)

	data, err := os.ReadFile(outputPath)
	test.That(t, err, test.ShouldBeNil)
	test.That(t, len(data), test.ShouldBeGreaterThan, 0)

	// Verify ftyp atom exists
	ftypOffset := findAtomOffset(data, "ftyp")
	test.That(t, ftypOffset, test.ShouldEqual, 0)

	// fMP4 structure without empty_moov:
	// [ftyp][moov][mdat][moof][mdat][moof][mdat]...
	// The moov contains initial sample tables, followed by fragmented data
	moovOffset := findAtomOffset(data, "moov")
	moofOffset := findAtomOffset(data, "moof")

	t.Logf("ftyp offset: %d, moov offset: %d, moof offset: %d",
		ftypOffset, moovOffset, moofOffset)

	// moov should exist near the start
	test.That(t, moovOffset, test.ShouldBeGreaterThan, -1)

	// moof (movie fragment) should exist for fMP4
	test.That(t, moofOffset, test.ShouldBeGreaterThan, -1)

	// moof should come after moov (fragments come after initial moov)
	test.That(t, moofOffset, test.ShouldBeGreaterThan, moovOffset)
}
