#include "../../videostore/utils.h"
#include <stdio.h>

int main(int argc, char *argv[]) {
  if (argc != 2) {
    printf("usage: %s <video file>\n", argv[0]);
    return 1;
  }
  int64_t duration;
  int ret = get_video_duration(&duration, argv[1]);
  if (ret == VIDEO_STORE_DURATION_RESP_OK) {
    printf("duration: %ld\n", duration);
  } else {
    printf("error getting duration\n");
  }
  return ret;
}