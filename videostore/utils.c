#include "utils.h"

int get_video_info(VideoInfo *info, const char *filename)
{
    info->duration = 0;
    info->width = 0;
    info->height = 0;
    info->codec[0] = '\0';

    AVFormatContext *fmt_ctx = NULL;
    int ret;

    if ((ret = avformat_open_input(&fmt_ctx, filename, NULL, NULL)) < 0) {
        return ret;
    }
    if ((ret = avformat_find_stream_info(fmt_ctx, NULL)) < 0) {
        avformat_close_input(&fmt_ctx);
        return ret;
    }

    info->duration = fmt_ctx->duration;
    if (info->duration == AV_NOPTS_VALUE) {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_VIDEO_INFO_RESP_ERROR;
    }

    for (unsigned i = 0; i < fmt_ctx->nb_streams; i++) {
        AVStream *st = fmt_ctx->streams[i];
        if (st->codecpar->codec_type == AVMEDIA_TYPE_VIDEO) {
            info->width  = st->codecpar->width;
            info->height = st->codecpar->height;
            const char *codecName = avcodec_get_name(st->codecpar->codec_id);
            if (codecName) {
                strncpy(info->codec, codecName, VIDEO_STORE_CODEC_NAME_LEN - 1);
                info->codec[VIDEO_STORE_CODEC_NAME_LEN - 1] = '\0';
            }
            break;
        }
    }

    if (info->width == 0 || info->height == 0 || info->codec[0] == '\0') {
        avformat_close_input(&fmt_ctx);
        return VIDEO_STORE_VIDEO_INFO_RESP_ERROR;
    }

    avformat_close_input(&fmt_ctx);
    return VIDEO_STORE_VIDEO_INFO_RESP_OK;
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
