#ifndef VIAM_RAW_SEGMENTER_H
#define VIAM_RAW_SEGMENTER_H
#include <libavformat/avformat.h>
typedef struct raw_seg {
  AVFormatContext *outCtx;
  // Callers pass packets without a duration. The mp4 muxer then gives each
  // segment's last frame a duration of 0, so every segment reports one frame
  // too short and concatenating segments drops a frame at each join. Packets
  // are stamped with the latest inter-frame DTS delta instead.
  int64_t lastDts;
  int64_t lastDuration;
  int hasLastDts;
} raw_seg;

int video_store_raw_seg_init_h264(struct raw_seg **ppRS,     // OUT
                                  const int segmentSeconds,  // IN
                                  const char *outputPattern, // IN
                                  const int width,           // IN
                                  const int height           // IN
);

int video_store_raw_seg_init_h265(struct raw_seg **ppRS,     // OUT
                                  const int segmentSeconds,  // IN
                                  const char *outputPattern, // IN
                                  const int width,           // IN
                                  const int height           // IN
);

int video_store_raw_seg_write_packet(struct raw_seg *rs,       // IN
                                     const char *payload,      // IN
                                     const size_t payloadSize, // IN
                                     const int64_t pts,        // IN
                                     const int64_t dts,        // IN
                                     const int isIdr           // IN
);

int video_store_raw_seg_close(struct raw_seg **rs // OUT
);
#define VIDEO_STORE_RAW_SEG_RESP_OK 0
#define VIDEO_STORE_RAW_SEG_RESP_ERROR 1
#endif /* VIAM_RAW_SEGMENTER_H */
