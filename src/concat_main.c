#include "concat.h"
#include <stdio.h>

int main(int argc, char *argv[]) {
  if (argc != 2) {
    printf("usage: %s <concat file> <output file>\n", argv[0]);
    return 1;
  }
  return video_store_concat(argv[1], argv[2]);
}
