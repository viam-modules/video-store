#include "../../videostore/concat.h"
#include <stdio.h>
#include <string.h>

int main(int argc, char *argv[]) {
  if (argc < 3 || argc > 4) {
    printf("usage: %s <concat file> <output file> [format]\n", argv[0]);
    printf("  format: mp4 (default), fmp4\n");
    return 1;
  }

  ContainerFormat format = CONTAINER_MP4;
  if (argc == 4) {
    if (strcmp(argv[3], "fmp4") == 0) {
      format = CONTAINER_FMP4;
    } else if (strcmp(argv[3], "mp4") != 0) {
      printf("unknown format: %s (use mp4 or fmp4)\n", argv[3]);
      return 1;
    }
  }

  return video_store_concat(argv[1], argv[2], format);
}
