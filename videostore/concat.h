#ifndef VIAM_CONCAT_H
#define VIAM_CONCAT_H

// Container format options for concat output
typedef enum {
  CONTAINER_DEFAULT = 0, // Same as CONTAINER_MP4
  CONTAINER_MP4 = 1,     // Standard MP4 with faststart (moov at beginning)
  CONTAINER_FMP4 = 2     // Fragmented MP4 for streaming
} ContainerFormat;

int video_store_concat(const char *concat_filepath, const char *output_path,
                       ContainerFormat container);

#define VIDEO_STORE_CONCAT_RESP_OK 0
#define VIDEO_STORE_CONCAT_RESP_ERROR 1
#endif /* VIAM_CONCAT_H */
