//go:build windows

package videostore

// Using local datetime format is only necessary on Windows systems
// where the FFmpeg is unable to use strftime to format filenames in
// unix timestamp format natively.
const outputPattern = "%Y-%m-%d_%H-%M-%S.mp4"
