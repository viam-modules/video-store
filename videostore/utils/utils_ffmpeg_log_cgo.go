package vsutils

/*
#include "utils.h"
#include <libavutil/log.h>
#include <stdio.h>

static void test_emit_av_log(int level, const char *msg) {
    av_log(NULL, level, "%s\n", msg);
    fflush(NULL);
}

static int test_get_av_log_level() {
    return av_log_get_level();
}

static void test_set_av_log_level(int level) {
    av_log_set_level(level);
}
*/
import "C"
import "unsafe"

// avLog* constants mirror libavutil/log.h integer values.
// These exist alongside SetLibAVLogLevel (which takes a string) so that
// tests can save/restore the exact numeric level via avLogLevel/setAVLogLevel
// without losing precision through the string mapping.
//
// This file cannot be a _test.go file: the Go toolchain prohibits CGO in test
// files. These symbols are package-private (unexported) to avoid leaking test
// helpers into the module's public API.
const (
	avLogFatal   = int(C.AV_LOG_FATAL)
	avLogError   = int(C.AV_LOG_ERROR)
	avLogWarning = int(C.AV_LOG_WARNING)
	avLogInfo    = int(C.AV_LOG_INFO)
	avLogVerbose = int(C.AV_LOG_VERBOSE)
	avLogDebug   = int(C.AV_LOG_DEBUG)
)

// emitAVLog triggers av_log at the given level. Intended for use in tests to
// exercise the custom log callback without going through a full FFmpeg operation.
func emitAVLog(level int, msg string) {
	cMsg := C.CString(msg)
	defer C.free(unsafe.Pointer(cMsg))
	C.test_emit_av_log(C.int(level), cMsg)
}

// getAVLogLevel returns the current global FFmpeg log level as an integer.
// Intended for use in tests to save and restore the log level around subtests.
func getAVLogLevel() int {
	return int(C.test_get_av_log_level())
}

// setAVLogLevel sets the global FFmpeg log level by integer value.
// Intended for use in tests to save and restore the log level around subtests.
func setAVLogLevel(level int) {
	C.test_set_av_log_level(C.int(level))
}
