#ifndef UTILS_H
#define UTILS_H

#include <libavformat/avformat.h>

// Function to get the duration of a video file
int64_t get_video_duration(const char *filename);

#endif // UTILS_H