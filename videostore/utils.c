#include "utils.h"

int get_video_duration(int64_t *duration, const char *filename) {
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
