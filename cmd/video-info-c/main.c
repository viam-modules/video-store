#include "../../videostore/utils.h"
#include <stdio.h>

int main(int argc, char *argv[]) {
  if (argc != 2) {
    printf("usage: %s <video file>\n", argv[0]);
    return 1;
  }
  video_store_video_info info;
  int ret = video_store_get_video_info(&info, argv[1]);
  if (ret == VIDEO_STORE_VIDEO_INFO_RESP_OK) {
    printf("duration: %ld, width: %d, height: %d, codec: %s\n",
       info.duration, info.width, info.height, info.codec);
  } else {
    printf("error getting video info\n");
  }
  return ret;
}
