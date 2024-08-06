SOURCE_OS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
SOURCE_ARCH ?= $(shell uname -m)
TARGET_OS ?= $(SOURCE_OS)
TARGET_ARCH ?= $(SOURCE_ARCH)
normalize_arch = $(if $(filter aarch64,$(1)),arm64,$(if $(filter x86_64,$(1)),amd64,$(1)))
SOURCE_ARCH := $(call normalize_arch,$(SOURCE_ARCH))
TARGET_ARCH := $(call normalize_arch,$(TARGET_ARCH))

BIN_OUTPUT_PATH = bin/$(TARGET_OS)-$(TARGET_ARCH)

FFMPEG_TAG ?= n6.1
FFMPEG_VERSION ?= $(shell pwd)/FFmpeg/$(FFMPEG_TAG)
FFMPEG_VERSION_PLATFORM ?= $(FFMPEG_VERSION)/$(TARGET_OS)-$(TARGET_ARCH)
FFMPEG_BUILD ?= $(FFMPEG_VERSION_PLATFORM)/build
FFMPEG_OPTS ?= --prefix=$(FFMPEG_BUILD) \
               --disable-shared \
               --disable-programs \
               --disable-doc \
               --disable-everything \
               --enable-static \
			   --enable-libx264 \
			   --enable-gpl \
               --enable-encoder=libx264 \
               --enable-muxer=segment \
               --enable-muxer=mp4 \
               --enable-demuxer=segment \
               --enable-protocol=file \
               --enable-protocol=concat \
               --enable-protocol=crypto

# TODO: cleanup libx264 static link
CGO_LDFLAGS := "-L$(FFMPEG_BUILD)/lib -lavcodec -lavutil -lavformat -l:libjpeg.a /usr/lib/aarch64-linux-gnu/libx264.a -lz"
CGO_CFLAGS := -I$(FFMPEG_BUILD)/include
export PKG_CONFIG_PATH=$(FFMPEG_BUILD)/lib/pkgconfig

$(BIN_OUTPUT_PATH)/filtered-video: *.go cam/*.go $(FFMPEG_BUILD)
	go mod tidy
	CGO_LDFLAGS=$(CGO_LDFLAGS) CGO_CFLAGS=$(CGO_CFLAGS) go build -o $(BIN_OUTPUT_PATH)/filtered-video main.go

$(FFMPEG_VERSION_PLATFORM):
	git clone https://github.com/FFmpeg/FFmpeg.git --depth 1 --branch $(FFMPEG_TAG) $(FFMPEG_VERSION_PLATFORM)

$(FFMPEG_BUILD): $(FFMPEG_VERSION_PLATFORM)
	cd $(FFMPEG_VERSION_PLATFORM) && ./configure $(FFMPEG_OPTS) && $(MAKE) -j$(NPROC) && $(MAKE) install