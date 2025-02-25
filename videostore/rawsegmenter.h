#ifndef VIAM_RAW_SEGMENTER_H
#define VIAM_RAW_SEGMENTER_H
#include <libavformat/avformat.h>
typedef struct raw_seg_h264 {
  AVFormatContext *outCtx;
} raw_seg_h264;

int video_store_raw_seg_init_h264(struct raw_seg_h264 **ppRS, // OUT
                                  const int segmentSeconds,   // IN
                                  const char *outputPattern,  // IN
                                  const char *extradata,      // IN
                                  const size_t extradataSize, // IN
                                  const int width,            // IN
                                  const int height            // IN
);

int video_store_raw_seg_write_h264_packet(struct raw_seg_h264 *rs,  // IN
                                          const char *payload,      // IN
                                          const size_t payloadSize, // IN
                                          const int64_t pts,        // IN
                                          const int isIdr           // IN
);

int video_store_raw_seg_close(struct raw_seg_h264 **rs // OUT
);
#define VIDEO_STORE_RAW_SEG_RESP_OK 0
#define VIDEO_STORE_RAW_SEG_RESP_ERROR 1
#endif /* VIAM_RAW_SEGMENTER_H */
