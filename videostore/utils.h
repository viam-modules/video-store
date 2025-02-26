#ifndef UTILS_H
#define UTILS_H
#include <libavformat/avformat.h>
int64_t get_video_duration(const char *filename);
#define VIDEO_STORE_DURATION_RESP_ERROR -1
#endif /* UTILS_H */
