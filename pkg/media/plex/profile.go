package plex

import (
	"strconv"
	"strings"
)

type Decoder struct {
	Width, Height int
}

type playable struct {
	container, video, audio string
}

var videoPlays = []playable{
	{container: "mkv", video: "h264,hevc", audio: "aac,opus,mp3,flac,vorbis"},
	{container: "mp4", video: "h264,hevc", audio: "aac,opus,mp3"},
	{container: "webm", video: "h264", audio: "aac,opus,vorbis"},
}

var limitedVideo = []string{"h264", "hevc"}

const decodedBits = 8

var containerTypes = map[string]string{
	"mkv":  "video/x-matroska",
	"mp4":  "video/mp4",
	"webm": "video/webm",
}

func (d Decoder) profile() string {
	var parts []string
	for _, p := range videoPlays {
		parts = append(parts, "add-direct-play-profile(type=videoProfile&container="+p.container+
			"&videoCodec="+p.video+"&audioCodec="+p.audio+"&subtitleCodec=*)")
	}
	limit := func(codec, name string, value int) string {
		return "add-limitation(scope=videoCodec&scopeName=" + codec +
			"&type=upperBound&name=" + name + "&value=" + strconv.Itoa(value) + "&isRequired=true)"
	}
	for _, codec := range limitedVideo {
		if d.Width > 0 {
			parts = append(parts, limit(codec, "video.width", d.Width))
		}
		if d.Height > 0 {
			parts = append(parts, limit(codec, "video.height", d.Height))
		}
		parts = append(parts, limit(codec, "video.bitDepth", decodedBits))
	}
	return strings.Join(parts, "+")
}
