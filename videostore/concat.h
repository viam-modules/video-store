#ifndef VIAM_CONCAT_H
#define VIAM_CONCAT_H
#include <stdint.h>
typedef enum {
  CONTAINER_DEFAULT = 0,
  CONTAINER_MP4     = 1,
  CONTAINER_FMP4    = 2
} container_t;
int video_store_concat(const char *concat_filepath, const char *output_path, container_t container);
#define VIDEO_STORE_CONCAT_RESP_OK 0
#define VIDEO_STORE_CONCAT_RESP_ERROR 1
#endif /* VIAM_CONCAT_H */
