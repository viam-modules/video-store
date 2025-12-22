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

func TestConcatContainerFormats(t *testing.T) {
	testCases := []struct {
		name      string
		container ContainerFormat
		// validateAtoms is called with the output data to verify container-specific atom layout
		validateAtoms func(t *testing.T, data []byte)
	}{
		{
			name:      "MP4",
			container: ContainerMP4,
			validateAtoms: func(t *testing.T, data []byte) {
				t.Helper()
				moovOffset := findAtomOffset(data, "moov")
				mdatOffset := findAtomOffset(data, "mdat")
				t.Logf("moov offset: %d, mdat offset: %d", moovOffset, mdatOffset)

				// With faststart, moov should come before mdat: [ftyp][moov][mdat]
				test.That(t, moovOffset, test.ShouldBeGreaterThan, -1)
				test.That(t, mdatOffset, test.ShouldBeGreaterThan, -1)
				test.That(t, moovOffset, test.ShouldBeLessThan, mdatOffset)
			},
		},
		{
			name:      "FMP4",
			container: ContainerFMP4,
			validateAtoms: func(t *testing.T, data []byte) {
				t.Helper()
				// fMP4 structure: [ftyp][moov][mdat][moof][mdat]...
				moovOffset := findAtomOffset(data, "moov")
				moofOffset := findAtomOffset(data, "moof")
				t.Logf("moov offset: %d, moof offset: %d", moovOffset, moofOffset)

				test.That(t, moovOffset, test.ShouldBeGreaterThan, -1)
				test.That(t, moofOffset, test.ShouldBeGreaterThan, -1)
				test.That(t, moofOffset, test.ShouldBeGreaterThan, moovOffset)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			logger := logging.NewTestLogger(t)
			outputDir := t.TempDir()
			storagePath := getArtifactStoragePath(t)

			c, err := newConcater(storagePath, outputDir, logger)
			test.That(t, err, test.ShouldBeNil)

			from := time.Unix(1725634803, 0)
			to := time.Unix(1725634850, 0)

			outputPath := filepath.Join(outputDir, tc.name+"_test.mp4")
			err = c.ConcatWithFormat(from, to, outputPath, tc.container)
			test.That(t, err, test.ShouldBeNil)

			data, err := os.ReadFile(outputPath)
			test.That(t, err, test.ShouldBeNil)
			test.That(t, len(data), test.ShouldBeGreaterThan, 0)

			// Verify ftyp atom exists at offset 0
			ftypOffset := findAtomOffset(data, "ftyp")
			test.That(t, ftypOffset, test.ShouldEqual, 0)

			tc.validateAtoms(t, data)
		})
	}
}
