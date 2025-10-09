#ifndef VIAM_CONCAT_H
#define VIAM_CONCAT_H
#include <stdint.h>
typedef void (*frame_callback_t)(uint8_t *data, int size, int stream_index, int64_t pts, void *user);
int video_store_concat(const char *concat_filepath, const char *output_path, const char *container);
#define VIDEO_STORE_CONCAT_RESP_OK 0
#define VIDEO_STORE_CONCAT_RESP_ERROR 1
#endif /* VIAM_CONCAT_H */
