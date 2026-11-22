#pragma once
#include <stdint.h>

#define AIRPLAY_VERSION 7

#define AIRPLAY_PORT 7000
#define AIRPLAY_MIRROR_PORT 7100
#define AIRPLAY_DATA_PORT 6000
#define AIRPLAY_CONTROL_PORT 6001
#define AIRPLAY_TIMING_PORT 7011
#define AIRPLAY_FD 3
#define AIRPLAY_MAX_PAYLOAD (4 << 20)

#define MSG_READY 'R'
#define MSG_CLIENT 'C'
#define MSG_MIRROR 'M'
#define MSG_FRAME 'V'
#define MSG_ENDED 'E'
#define MSG_DISCONNECT 'X'
#define MSG_AUDIO 'A'
#define MSG_SOUND 'S'
#define MSG_PLAY 'P'
#define MSG_SCRUB 'K'
#define MSG_RATE 'T'
#define MSG_STOP 'Z'
#define MSG_POSITION 'O'
#define MSG_PIN 'N'
#define MSG_PAIRED 'D'
#define MSG_VOLUME 'L'
#define MSG_MUTE 'U'
#define MSG_LEVEL 'B'

#define CODEC_H264 1
#define CODEC_H265 2

#define STATE_LOADING 0
#define STATE_PLAYING 1
#define STATE_PAUSED 2
#define STATE_OVER 3
#define STATE_ENDED 4

struct airplay_header {
    uint32_t kind;
    uint32_t length;
};

struct airplay_ready {
    uint32_t version;
    uint32_t port;
    uint32_t raop_name;
    uint32_t airplay_name;
    uint32_t raop_txt;
    uint32_t airplay_txt;
};

struct airplay_mirror {
    uint32_t codec;
    uint32_t width;
    uint32_t height;
};

struct airplay_audio {
    uint32_t rate;
    uint32_t channels;
    int32_t due_us;
};

struct airplay_frame {
    uint64_t at_us;
    uint32_t width;
    uint32_t height;
};

struct airplay_play {
    int64_t start_us;
};

struct airplay_scrub {
    int64_t at_us;
};

struct airplay_rate {
    uint32_t playing;
};

struct airplay_volume {
    float db;
};

struct airplay_mute {
    uint32_t muted;
};

struct airplay_position {
    int64_t at_us;
    int64_t length_us;
    int64_t state;
};
