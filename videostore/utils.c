#include "utils.h"

int64_t get_video_duration(const char *filename) {
    AVFormatContext *fmt_ctx = NULL;
    if (avformat_open_input(&fmt_ctx, filename, NULL, NULL) < 0) {
        return VIDEO_STORE_DURATION_RESP_ERROR;
    }
    if (avformat_find_stream_info(fmt_ctx, NULL) < 0) {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_DURATION_RESP_ERROR;
    }
    int64_t duration = fmt_ctx->duration;
    avformat_close_input(&fmt_ctx);
    return duration;
}
