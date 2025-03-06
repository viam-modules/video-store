#ifndef VIAM_VIDEOSTORE_UTILS_H
#define VIAM_VIDEOSTORE_UTILS_H
#include <libavformat/avformat.h>
int get_video_duration(int64_t *duration, const char *filename);
void custom_av_log_callback(void *ptr, int level, const char *fmt, va_list vargs);
void set_custom_av_log_callback();
void test_av_log();
#define VIDEO_STORE_DURATION_RESP_ERROR 1
#define VIDEO_STORE_DURATION_RESP_OK 0
#endif /* VIAM_VIDEOSTORE_UTILS_H */
