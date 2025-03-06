#include "utils.h"

int get_video_duration(int64_t *duration,   // OUT
                       const char *filename // IN
) {
    AVFormatContext *fmt_ctx = NULL;
    int ret;
    if ((ret = avformat_open_input(&fmt_ctx, filename, NULL, NULL)) < 0) {
        return ret;
    }
    if ((ret = avformat_find_stream_info(fmt_ctx, NULL)) < 0) {
        avformat_close_input(&fmt_ctx);
    return ret;
    }
    *duration = fmt_ctx->duration;
    if (*duration == AV_NOPTS_VALUE) {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_DURATION_RESP_ERROR;
    }
    avformat_close_input(&fmt_ctx);
    return VIDEO_STORE_DURATION_RESP_OK;
}

void custom_av_log_callback(void *ptr, int level, const char *fmt, va_list vargs) {
    // Default callback will handle log level filtering
    av_log_default_callback(ptr, level, fmt, vargs);
    // Default callback only prints to stderr
    fflush(stderr);
}

void set_custom_av_log_callback() {
    av_log_set_callback(custom_av_log_callback);
}
