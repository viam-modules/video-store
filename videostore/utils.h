#ifndef VIAM_VIDEOSTORE_UTILS_H
#define VIAM_VIDEOSTORE_UTILS_H
#include <libavformat/avformat.h>
int get_video_duration(int64_t *duration, const char *filename);
#define VIDEO_STORE_DURATION_RESP_ERROR 1
#define VIDEO_STORE_DURATION_RESP_OK 0
#endif /* VIAM_VIDEOSTORE_UTILS_H */
