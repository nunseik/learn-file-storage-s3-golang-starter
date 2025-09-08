package main

import (
	"bytes"
	"encoding/json"
	"math"
	"os/exec"
)

func isAspectRatioApprox(w, h, rw, rh int, relTol float64) bool {
	r := float64(w) / float64(h)
	target := float64(rw) / float64(rh)
	return math.Abs(r-target) <= relTol*target
}
func getVideoAspectRatio(filePath string) (string, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-print_format", "json", "-show_streams", filePath)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", err
	}

	type Stream struct {
		CodecType string `json:"codec_type"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
	}
	type VideoAspect struct {
		Streams []Stream `json:"streams"`
	}
	var videoData VideoAspect
	if err := json.Unmarshal(out.Bytes(), &videoData); err != nil {
		return "", err
	}
	// pick the first video
	var vw, vh int
	for _, stream := range videoData.Streams {
		if stream.CodecType == "video" {
			vw, vh = stream.Width, stream.Height
			break
		}
	}
	// Compare with tolerance (2% here)
	if isAspectRatioApprox(vw, vh, 16, 9, 0.02) {
		return "landscape", nil
	}
	if isAspectRatioApprox(vw, vh, 9, 16, 0.02) {
		return "portrait", nil
	}
	return "other", nil
}
